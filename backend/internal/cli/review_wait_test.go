package cli

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func reviewsJSON(pr int, runs ...string) string {
	body := ""
	for i, r := range runs {
		if i > 0 {
			body += ","
		}
		body += r
	}
	return fmt.Sprintf(`{"reviews":[{"prNumber":%d,"status":"x","currentHeadRuns":[%s]}]}`, pr, body)
}

func run(status, verdict, harness string) string {
	return fmt.Sprintf(`{"id":"r-%s","status":%q,"verdict":%q,"harness":%q}`, harness, status, verdict, harness)
}

func waitServer(t *testing.T, bodies ...string) *httptest.Server {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := int(n.Add(1)) - 1
		if i >= len(bodies) {
			i = len(bodies) - 1
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(bodies[i]))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func runWait(t *testing.T, srv *httptest.Server, args ...string) (string, error) {
	t.Helper()
	cfg := setReviewEnv(t)
	writeRunFileFor(t, cfg, srv)
	old := reviewWaitPollInterval
	reviewWaitPollInterval = 5 * time.Millisecond
	t.Cleanup(func() { reviewWaitPollInterval = old })
	out, _, err := executeCLI(t, aliveDeps(), append([]string{"review", "wait", "mer-1"}, args...)...)
	return out, err
}

func TestReviewWaitReturnsOnVerdictWithMatchingExitCode(t *testing.T) {
	for _, tc := range []struct {
		name, final, out string
		code             int
	}{
		{"approved", run("complete", "approved", "claude-code"), "approved pr=7 head=-\n", 0},
		{"changes", run("complete", "changes_requested", "claude-code"), "changes_requested pr=7 head=-\n", exitReviewChangesRequested},
		{"failed", run("failed", "", "claude-code"), "no_verdict pr=7 head=-\n", exitReviewNoVerdict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := waitServer(t, reviewsJSON(7, run("running", "", "claude-code")), reviewsJSON(7, run("running", "", "claude-code")), reviewsJSON(7, tc.final))
			out, err := runWait(t, srv)
			if out != tc.out || ExitCode(err) != tc.code {
				t.Fatalf("out=%q code=%d err=%v, want %q / %d", out, ExitCode(err), err, tc.out, tc.code)
			}
		})
	}
}

func TestReviewWaitStaysWhileAnyReviewerStillRuns(t *testing.T) {
	srv := waitServer(t,
		reviewsJSON(7, run("running", "", "codex"), run("complete", "approved", "claude-code")),
		reviewsJSON(7, run("complete", "approved", "codex"), run("complete", "approved", "claude-code")))
	out, err := runWait(t, srv)
	if out != "approved pr=7 head=-\n" || err != nil {
		t.Fatalf("out=%q err=%v: returned while a reviewer was still running", out, err)
	}
}

func TestReviewWaitTimesOut(t *testing.T) {
	srv := waitServer(t, reviewsJSON(7, run("running", "", "claude-code")))
	out, err := runWait(t, srv, "--timeout", "40ms")
	if out != "timeout\n" || ExitCode(err) != exitReviewTimeout {
		t.Fatalf("out=%q code=%d err=%v, want timeout / %d", out, ExitCode(err), err, exitReviewTimeout)
	}
}

func TestReviewWaitPRFilterIgnoresOtherPRs(t *testing.T) {
	body := `{"reviews":[{"prNumber":7,"currentHeadRuns":[` + run("running", "", "a") + `]},{"prNumber":8,"currentHeadRuns":[` + run("complete", "approved", "a") + `]}]}`
	srv := waitServer(t, body)
	out, err := runWait(t, srv, "--pr", "8")
	if out != "approved pr=8 head=-\n" || err != nil {
		t.Fatalf("out=%q err=%v: --pr 8 waited on PR 7", out, err)
	}
}

// After a push the PR's head moves on; the latest run is still the old head's
// changes_requested, and waiting for the fix to be reviewed must not return it.
func TestReviewWaitIgnoresVerdictOfAnOlderHead(t *testing.T) {
	stale := `{"reviews":[{"prNumber":7,"targetSha":"bbb","latestRun":{"id":"r-old","status":"complete","verdict":"changes_requested","targetSha":"aaa"}}]}`
	fresh := `{"reviews":[{"prNumber":7,"targetSha":"bbb","currentHeadRuns":[{"id":"r-new","status":"complete","verdict":"approved","targetSha":"bbb"}]}]}`
	srv := waitServer(t, stale, stale, fresh)
	out, err := runWait(t, srv)
	if out != "approved pr=7 head=bbb\n" || err != nil {
		t.Fatalf("out=%q err=%v: returned the previous head's verdict", out, err)
	}
}

const mergedApproved = `{"prNumber":317,"status":"ineligible","targetSha":"m1","latestRun":{"id":"r-m","status":"complete","verdict":"approved","targetSha":"m1"}}`

func openPR(n int, sha string, runs ...string) string {
	body := ""
	for i, r := range runs {
		if i > 0 {
			body += ","
		}
		body += r
	}
	return fmt.Sprintf(`{"prNumber":%d,"status":"running","targetSha":%q,"currentHeadRuns":[%s]}`, n, sha, body)
}

func shaRun(status, verdict, sha string) string {
	return fmt.Sprintf(`{"id":"r-%s","status":%q,"verdict":%q,"targetSha":%q}`, sha, status, verdict, sha)
}

func TestReviewWaitIgnoresMergedPRApprovalAndBlocksOnOpenPR(t *testing.T) {
	running := `{"reviews":[` + mergedApproved + `,` + openPR(320, "n1", shaRun("running", "", "n1")) + `]}`
	done := `{"reviews":[` + mergedApproved + `,` + openPR(320, "n1", shaRun("complete", "changes_requested", "n1")) + `]}`
	srv := waitServer(t, running, running, done)
	out, err := runWait(t, srv)
	if out != "changes_requested pr=320 head=n1\n" || ExitCode(err) != exitReviewChangesRequested {
		t.Fatalf("out=%q err=%v: returned the merged PR's approval", out, err)
	}
}

func TestReviewWaitNoRunForCurrentHeadExitsDistinctly(t *testing.T) {
	old := reviewWaitNoRunGrace
	reviewWaitNoRunGrace = 20 * time.Millisecond
	t.Cleanup(func() { reviewWaitNoRunGrace = old })
	body := `{"reviews":[` + mergedApproved + `,{"prNumber":320,"status":"needs_review","targetSha":"n2","latestRun":` + shaRun("complete", "approved", "n1") + `}]}`
	out, err := runWait(t, waitServer(t, body))
	if out != "no_run\n" || ExitCode(err) != exitReviewNoRun {
		t.Fatalf("out=%q code=%d err=%v, want no_run / %d", out, ExitCode(err), err, exitReviewNoRun)
	}
}

func TestReviewWaitPRSelectsOneOpenPR(t *testing.T) {
	body := `{"reviews":[` + openPR(5, "a", shaRun("running", "", "a")) + `,` + openPR(6, "b", shaRun("complete", "approved", "b")) + `]}`
	out, err := runWait(t, waitServer(t, body), "--pr", "6")
	if out != "approved pr=6 head=b\n" || err != nil {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestReviewWaitSeveralOpenPRsWaitsForEach(t *testing.T) {
	first := `{"reviews":[` + openPR(5, "a", shaRun("complete", "approved", "a")) + `,` + openPR(6, "b", shaRun("running", "", "b")) + `]}`
	last := `{"reviews":[` + openPR(5, "a", shaRun("complete", "approved", "a")) + `,` + openPR(6, "b", shaRun("complete", "approved", "b")) + `]}`
	out, err := runWait(t, waitServer(t, first, first, last))
	if out != "approved pr=5 head=a\napproved pr=6 head=b\n" || err != nil {
		t.Fatalf("out=%q err=%v", out, err)
	}
}
