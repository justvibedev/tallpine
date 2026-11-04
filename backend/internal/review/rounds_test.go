package review

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestChangesRequestedRoundsResetAtApproval(t *testing.T) {
	now := time.Now()
	run := func(sha string, v domain.ReviewVerdict, ago time.Duration) domain.ReviewRun {
		return domain.ReviewRun{PRURL: "pr", TargetSHA: sha, Verdict: v, CreatedAt: now.Add(-ago)}
	}
	runs := []domain.ReviewRun{
		run("a", domain.VerdictChangesRequested, 4*time.Hour), run("b", domain.VerdictChangesRequested, 3*time.Hour),
		run("c", domain.VerdictApproved, 2*time.Hour), run("d", domain.VerdictChangesRequested, time.Hour),
	}
	if got := ChangesRequestedRounds(runs, "pr"); got != 1 {
		t.Fatalf("rounds = %d, want 1 (only the change request after the approval)", got)
	}
	if got := ChangesRequestedRounds(runs[:2], "pr"); got != 2 {
		t.Fatalf("rounds without an approval = %d, want 2", got)
	}
}

// An exhausted PR must not be reviewed by an automatic pass that is started for
// another PR on the same worker.
func TestAutoTriggerSkipsExhaustedPRWhenAnotherIsDue(t *testing.T) {
	const pr1, pr2 = "https://github.com/o/r/pull/1", "https://github.com/o/r/pull/2"
	cr := func(sha string, ago time.Duration) domain.ReviewRun {
		return domain.ReviewRun{ID: "r-" + sha, ReviewID: "rv1", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode, PRURL: pr1, TargetSHA: sha, Status: domain.ReviewRunComplete, Verdict: domain.VerdictChangesRequested, TriggerSource: domain.ReviewTriggerAuto, CreatedAt: time.Now().Add(-ago)}
	}
	store := &fakeStore{runs: []domain.ReviewRun{cr("a", 2*time.Hour), cr("b", time.Hour)}}
	prs := fakePRs{prs: []domain.PullRequest{{URL: pr1, Number: 1, HeadSHA: "c", TargetBranch: "main"}, {URL: pr2, Number: 2, HeadSHA: "x", TargetBranch: "main"}}}
	cfg := domain.ProjectConfig{AutoReviewMaxRounds: 2}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prs, fakeProjects{cfg: cfg}, &fakeLauncher{handle: "review-mer-1"})
	eng.changedPaths = func(context.Context, string, domain.PullRequest) ([]string, error) { return nil, nil }
	if _, err := triggerWithSource(context.Background(), eng, "mer-1", domain.ReviewerClaudeCode, domain.AgentConfig{}, domain.ReviewTriggerAuto); err != nil {
		t.Fatal(err)
	}
	var reviewed []string
	for _, r := range store.runs {
		if r.Status == domain.ReviewRunRunning {
			reviewed = append(reviewed, r.PRURL)
		}
	}
	if len(reviewed) != 1 || reviewed[0] != pr2 {
		t.Fatalf("reviewed PRs = %v, want only %s", reviewed, pr2)
	}
}
