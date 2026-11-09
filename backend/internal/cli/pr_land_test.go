package cli

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

const landNewHead = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type landRunner struct {
	state     string
	head      string
	diffOld   string
	diffNew   string
	didMerge  bool
	mainFiles string
	prFiles   string
	calls     []string
}

func (r *landRunner) run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	switch {
	case name == "gh" && args[1] == "view":
		return []byte(fmt.Sprintf(`{"number":42,"headRefOid":%q,"baseRefName":"main","mergeStateStatus":%q}`, r.head, r.state)), nil
	case name == "gh" && args[1] == "merge":
		panic("pr land must merge via the daemon, not gh")
	case name == "git" && args[0] == "merge-base":
		return []byte("base-" + args[1][:4] + "\n"), nil
	case name == "git" && args[0] == "diff" && args[1] == "--name-only":
		if strings.HasPrefix(args[2], "base-"+testHeadSHA[:4]) {
			return []byte(r.mainFiles), nil
		}
		return []byte(r.prFiles), nil
	case name == "git" && args[0] == "diff":
		if strings.HasPrefix(args[len(args)-1], testHeadSHA) {
			return []byte(r.diffOld), nil
		}
		return []byte(r.diffNew), nil
	}
	return nil, nil
}

func (r *landRunner) merged() bool { return r.didMerge }

func landRoutes(reviews string) map[string]mergeRoute {
	return map[string]mergeRoute{
		"GET /api/v1/sessions":                {200, `{"sessions":[{"id":"s1","prs":[{"url":"https://github.com/o/r/pull/42","number":42}]}]}`},
		"GET /api/v1/sessions/s1/reviews":     {200, reviews},
		"POST /api/v1/prs/42/merge":           {200, `{"ok":true,"prNumber":42}`},
		"POST /api/v1/prs/42/merge-preflight": {200, `{"ok":true,"prNumber":42}`},
		"POST /api/v1/sessions/s1/kill":       {200, `{"sessionId":"s1","freed":true}`},
	}
}

const approvedReviews = `{"reviews":[{"prUrl":"https://github.com/o/r/pull/42","latestRun":{"id":"r1","verdict":"approved","targetSha":"` + testHeadSHA + `"}}]}`

func runLand(t *testing.T, routes map[string]mergeRoute, r *landRunner, args ...string) (string, *[]string, error) {
	t.Helper()
	out, reqs, _, err := runLandBody(t, routes, r, args...)
	return out, reqs, err
}

func runLandBody(t *testing.T, routes map[string]mergeRoute, r *landRunner, args ...string) (string, *[]string, *string, error) {
	t.Helper()
	cfg := setConfigEnv(t)
	srv, reqs, mergeBody := mergeDaemon(t, routes)
	writeRunFileFor(t, cfg, srv)
	deps := aliveDeps()
	deps.CommandOutput = r.run
	out, _, err := executeCLI(t, deps, append([]string{"pr", "land", "42", "--session", "s1"}, args...)...)
	r.didMerge = contains(*reqs, "POST /api/v1/prs/42/merge")
	return out, reqs, mergeBody, err
}

func TestPRLandSameHeadMerges(t *testing.T) {
	r := &landRunner{state: "CLEAN", head: testHeadSHA}
	out, reqs, mergeBody, err := runLandBody(t, landRoutes(approvedReviews), r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(*mergeBody, `"expectedHeadSha":"`+testHeadSHA+`"`) || !strings.Contains(*mergeBody, "pull/42") {
		t.Fatalf("merge body = %s", *mergeBody)
	}
	if !contains(*reqs, "POST /api/v1/prs/42/merge") || contains(*reqs, "POST /api/v1/sessions/s1/kill") || !strings.Contains(out, "merged PR #42") {
		t.Fatalf("reqs=%v out=%q", *reqs, out)
	}
}

func TestPRLandIdenticalRebasedPatchAccepted(t *testing.T) {
	d1 := "diff --git a/x b/x\nindex 111..222 100644\n--- a/x\n+++ b/x\n@@ -1,2 +1,3 @@\n+a\n"
	d2 := "diff --git a/x b/x\nindex 333..444 100644\n--- a/x\n+++ b/x\n@@ -10,2 +10,3 @@\n+a\n"
	r := &landRunner{state: "CLEAN", head: landNewHead, diffOld: d1, diffNew: d2}
	if _, _, err := runLand(t, landRoutes(approvedReviews), r); err != nil {
		t.Fatal(err)
	}
	if !r.merged() {
		t.Fatalf("not merged: %v", r.calls)
	}
}

func TestPRLandDifferingPatchRefused(t *testing.T) {
	r := &landRunner{state: "CLEAN", head: landNewHead, diffOld: "+a\n", diffNew: "+b\n"}
	_, _, err := runLand(t, landRoutes(approvedReviews), r)
	if err == nil || !strings.Contains(err.Error(), "patch changed") || r.merged() {
		t.Fatalf("err=%v calls=%v", err, r.calls)
	}
}

func TestPRLandNotApprovedRefused(t *testing.T) {
	r := &landRunner{state: "CLEAN", head: testHeadSHA}
	reviews := `{"reviews":[{"prUrl":"https://github.com/o/r/pull/42","latestRun":{"id":"r1","verdict":"changes_requested","targetSha":"` + testHeadSHA + `"}}]}`
	_, _, err := runLand(t, landRoutes(reviews), r)
	if err == nil || !strings.Contains(err.Error(), "no approved review") || r.merged() {
		t.Fatalf("err=%v", err)
	}
}

func TestPRLandNonCleanRefused(t *testing.T) {
	r := &landRunner{state: "BEHIND", head: testHeadSHA}
	_, _, err := runLand(t, landRoutes(approvedReviews), r)
	if err == nil || !strings.Contains(err.Error(), "CLEAN") || r.merged() {
		t.Fatalf("err=%v", err)
	}
}

func TestPRLandDryRunDoesNotMergeOrKill(t *testing.T) {
	r := &landRunner{state: "CLEAN", head: testHeadSHA}
	out, reqs, _, err := runLandBody(t, landRoutes(approvedReviews), r, "--dry-run", "--kill-worker")
	if err != nil || !contains(*reqs, "POST /api/v1/prs/42/merge-preflight") || contains(*reqs, "POST /api/v1/prs/42/merge") || contains(*reqs, "POST /api/v1/sessions/s1/kill") || !strings.Contains(out, "dry-run: would squash-merge") {
		t.Fatalf("err=%v out=%q calls=%v reqs=%v", err, out, r.calls, *reqs)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

var _ = http.StatusOK

const landRebaseDiff = "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n+a\n"

func TestPRLandOverlapRefused(t *testing.T) {
	r := &landRunner{state: "CLEAN", head: landNewHead, diffOld: landRebaseDiff, diffNew: landRebaseDiff, mainFiles: "x\ny\n", prFiles: "x\n"}
	_, _, err := runLand(t, landRoutes(approvedReviews), r)
	if err == nil || !strings.Contains(err.Error(), "also touches") || !strings.Contains(err.Error(), "x") || r.merged() {
		t.Fatalf("err=%v calls=%v", err, r.calls)
	}
}

func TestPRLandOverlapAllowed(t *testing.T) {
	r := &landRunner{state: "CLEAN", head: landNewHead, diffOld: landRebaseDiff, diffNew: landRebaseDiff, mainFiles: "x\n", prFiles: "x\n"}
	if _, _, err := runLand(t, landRoutes(approvedReviews), r, "--allow-overlap"); err != nil || !r.merged() {
		t.Fatalf("err=%v calls=%v", err, r.calls)
	}
}

func TestPRLandNoOverlapProceeds(t *testing.T) {
	r := &landRunner{state: "CLEAN", head: landNewHead, diffOld: landRebaseDiff, diffNew: landRebaseDiff, mainFiles: "y\n", prFiles: "x\n"}
	if _, _, err := runLand(t, landRoutes(approvedReviews), r); err != nil || !r.merged() {
		t.Fatalf("err=%v calls=%v", err, r.calls)
	}
}

func TestPRLandKillWorkerAfterMerge(t *testing.T) {
	r := &landRunner{state: "CLEAN", head: testHeadSHA}
	_, reqs, err := runLand(t, landRoutes(approvedReviews), r, "--kill-worker")
	n := len(*reqs)
	if err != nil || n < 2 || (*reqs)[n-2] != "POST /api/v1/prs/42/merge" || (*reqs)[n-1] != "POST /api/v1/sessions/s1/kill" {
		t.Fatalf("err=%v reqs=%v", err, *reqs)
	}
}

const landBlockedBody = `{"message":"PR merge preconditions are not met: review approval is required","code":"PR_PRECONDITIONS_UNMET","details":{"reason":"review_approval_required","detail":"review approval is required"}}`

// B597: an identical rebase where the daemon gate does not apply merges, and the
// dry run reports the same daemon answer instead of its own patch comparison.
func TestPRLandIdenticalRebaseDryRunAndRealAgreeWhenGateApplies(t *testing.T) {
	for _, args := range [][]string{nil, {"--dry-run"}} {
		routes := landRoutes(approvedReviews)
		routes["POST /api/v1/prs/42/merge"] = mergeRoute{422, landBlockedBody}
		routes["POST /api/v1/prs/42/merge-preflight"] = mergeRoute{422, landBlockedBody}
		r := &landRunner{state: "CLEAN", head: landNewHead, diffOld: landRebaseDiff, diffNew: landRebaseDiff}
		_, reqs, _, err := runLandBody(t, routes, r, args...)
		if err == nil || !strings.Contains(err.Error(), "review_approval_required") {
			t.Fatalf("args=%v err=%v", args, err)
		}
		if len(args) > 0 && (!contains(*reqs, "POST /api/v1/prs/42/merge-preflight") || contains(*reqs, "POST /api/v1/prs/42/merge")) {
			t.Fatalf("dry-run must use only the preflight route: %v", *reqs)
		}
	}
}

func TestPRLandIdenticalRebaseDryRunAndRealAgreeWhenGateDoesNotApply(t *testing.T) {
	for _, args := range [][]string{nil, {"--dry-run"}} {
		r := &landRunner{state: "CLEAN", head: landNewHead, diffOld: landRebaseDiff, diffNew: landRebaseDiff}
		out, _, err := runLand(t, landRoutes(approvedReviews), r, args...)
		if err != nil || (len(args) == 0 && !r.merged()) || (len(args) > 0 && !strings.Contains(out, "would squash-merge")) {
			t.Fatalf("args=%v err=%v out=%q", args, err, out)
		}
	}
}
