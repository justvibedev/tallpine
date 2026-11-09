package cli

import (
	"net/http"
	"strings"
	"testing"
)

func TestReviewListDefaultsToCallingSession(t *testing.T) {
	cfg := setReviewEnv(t)
	t.Setenv("TALLPINE_SESSION_ID", "mer-3")
	srv, capture := reviewServer(t, http.StatusOK, `{"reviews":[]}`)
	writeRunFileFor(t, cfg, srv)

	if _, errOut, err := executeCLI(t, aliveDeps(), "review", "ls"); err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if capture.path != "/api/v1/sessions/mer-3/reviews" {
		t.Fatalf("path = %q, want the calling session", capture.path)
	}
}

func TestReviewTriggerShowsModelAndRiskReason(t *testing.T) {
	cfg := setReviewEnv(t)
	t.Setenv("TALLPINE_SESSION_ID", "mer-9")
	srv, _ := reviewServer(t, http.StatusCreated, `{"created":true,"reviews":[{"prUrl":"https://github.com/acme/app/pull/4","prNumber":4,"status":"running","latestRun":{"id":"run-4","harness":"claude-code","model":"claude-opus-5-5","modelReason":"risky path backend/internal/httpd/a.go matches backend/internal/httpd/**","prUrl":"https://github.com/acme/app/pull/4","targetSha":"0123456789abcdef","status":"running"}}]}`)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, aliveDeps(), "review", "trigger")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	want := "claude-code (claude-opus-5-5) [risky path backend/internal/httpd/a.go matches backend/internal/httpd/**] reviewing"
	if !strings.Contains(out, want) {
		t.Fatalf("stdout = %q, want %q", out, want)
	}
}

func TestReviewListShowsRunningPassOnOlderHead(t *testing.T) {
	cfg := setReviewEnv(t)
	body := `{"reviews":[{"prUrl":"https://github.com/a/b/pull/7","prNumber":7,"title":"T","targetSha":"newsha1234567890","status":"needs_review","olderHeadRun":{"id":"run-old","harness":"claude-code","prUrl":"https://github.com/a/b/pull/7","targetSha":"oldsha1234567890","status":"running"}}]}`
	srv, _ := reviewServer(t, http.StatusOK, body)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, aliveDeps(), "review", "ls", "mer-1")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	want := "#7: claude-code still reviewing oldsha123456, head moved to newsha123456 (run run-old)"
	if !strings.Contains(out, want) {
		t.Fatalf("stdout = %q, want %q", out, want)
	}
}

// B590: a verdict that never reached the PR is visible in `review ls`.
func TestReviewListShowsRunNotPostedToPR(t *testing.T) {
	cfg := setReviewEnv(t)
	srv, _ := reviewServer(t, http.StatusOK, `{
		"reviewerHandleId":"handle-1",
		"reviews":[{
			"prUrl":"https://github.com/example/repo/pull/42",
			"prNumber":42,
			"title":"Fix session resume",
			"targetSha":"abc123",
			"status":"changes_requested",
			"latestRun":{"id":"run-1","reviewId":"review-1","harness":"codex","status":"complete","verdict":"changes_requested","postFailed":true}
		}]
	}`)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, aliveDeps(), "review", "ls", "mer-1")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if want := "#42 codex: not posted to the PR"; !strings.Contains(out, want) {
		t.Fatalf("stdout = %q, want %q", out, want)
	}
}

func TestReviewListShowsBranchForNoPRReview(t *testing.T) {
	cfg := setReviewEnv(t)
	srv, _ := reviewServer(t, http.StatusOK, `{"reviews":[{"prUrl":"local:tp/w1/root","prNumber":0,"status":"needs_review","targetSha":"0123456789abcdef"}]}`)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, aliveDeps(), "review", "ls", "mer-1")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if strings.Contains(out, "#0") || !strings.Contains(out, "branch tp/w1/root") {
		t.Fatalf("stdout = %q, want the branch instead of #0", out)
	}
}
