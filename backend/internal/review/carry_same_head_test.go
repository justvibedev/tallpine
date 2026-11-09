package review

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// An approval on this very head by another reviewer is not a rebase: it must
// not be "carried" to the requested reviewer as if that reviewer had run.
func TestCarryIgnoresApprovalOnTheSameHead(t *testing.T) {
	const url = "https://github.com/o/r/pull/1"
	prior := domain.ReviewRun{ID: "r-old", ReviewID: "rv1", SessionID: "mer-1", Harness: domain.ReviewerCodex, PRURL: url, TargetSHA: "sha1",
		Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved, CreatedAt: time.Now().Add(-time.Hour)}
	store := &fakeStore{runs: []domain.ReviewRun{prior}}
	prs := fakePRs{prs: []domain.PullRequest{{URL: url, Number: 1, HeadSHA: "sha1", TargetBranch: "main"}}}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prs, fakeProjects{cfg: domain.ProjectConfig{ApproveIdenticalPatch: true}}, &fakeLauncher{handle: "review-mer-1"})
	eng.changedPaths = func(context.Context, string, domain.PullRequest) ([]string, error) { return nil, nil }
	eng.patchID = func(context.Context, string, string, string) (string, error) { return "p", nil }
	if _, err := trigger(context.Background(), eng, "mer-1", domain.ReviewerClaudeCode, domain.AgentConfig{}); err != nil {
		t.Fatal(err)
	}
	for _, r := range store.runs {
		if r.ID != "r-old" && r.Verdict == domain.VerdictApproved {
			t.Fatalf("a review that never ran was recorded as approved: %+v", r)
		}
	}
}
