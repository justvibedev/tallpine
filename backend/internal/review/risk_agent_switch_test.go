package review

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

const (
	prA = "https://github.com/o/r/pull/1"
	prB = "https://github.com/o/r/pull/2"
)

// switchEngine has two PRs: A changes a safe path, B a risky one, with Codex as
// the project's stronger reviewer.
func switchEngine(runs []domain.ReviewRun) (*Engine, *fakeStore, *fakeLauncher) {
	store := &fakeStore{runs: runs}
	launcher := &fakeLauncher{handle: "h"}
	prs := fakePRs{prs: []domain.PullRequest{{URL: prA, Number: 1, HeadSHA: "a"}, {URL: prB, Number: 2, HeadSHA: "b"}}}
	cfg := domain.ProjectConfig{ReviewRisk: domain.ReviewRiskConfig{Paths: riskPaths, Harness: domain.ReviewerCodex, Model: "gpt-6.1-sol"}}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prs, fakeProjects{cfg: cfg}, launcher)
	eng.changedPaths = func(_ context.Context, _ string, pr domain.PullRequest) ([]string, error) {
		if pr.URL == prB {
			return []string{"backend/internal/httpd/a.go"}, nil
		}
		return []string{"frontend/a.ts"}, nil
	}
	return eng, store, launcher
}

func approved(h domain.ReviewerHarness, url, sha string) domain.ReviewRun {
	return domain.ReviewRun{ID: "r-" + string(h) + sha, ReviewID: "rv", SessionID: "mer-1", Harness: h, PRURL: url, TargetSHA: sha, Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved}
}

func startedFor(store *fakeStore, url string) int {
	n := 0
	for _, r := range store.runs {
		if r.PRURL == url && r.Status == domain.ReviewRunRunning {
			n++
		}
	}
	return n
}

// The auto-review coordinator names the project's own reviewer on every pass; that
// is not an explicit choice, so a risky PR still gets the stronger reviewer.
func TestAutoReviewOfRiskyPRGetsStrongerReviewer(t *testing.T) {
	eng, _, launcher := switchEngine(nil)
	if _, err := triggerWithSource(context.Background(), eng, "mer-1", domain.ReviewerClaudeCode, domain.AgentConfig{}, domain.ReviewTriggerAuto); err != nil {
		t.Fatal(err)
	}
	if launcher.gotSpec.Harness != domain.ReviewerCodex || launcher.gotSpec.AgentConfig.Model != "gpt-6.1-sol" {
		t.Fatalf("auto pass on %q model %q, want codex gpt-6.1-sol", launcher.gotSpec.Harness, launcher.gotSpec.AgentConfig.Model)
	}
}

// A stronger agent reviews only the risky PRs: the safe PR A is left to the
// normal reviewer rather than reviewed by Codex too.
func TestStrongerAgentPassReviewsOnlyRiskyPRs(t *testing.T) {
	eng, store, launcher := switchEngine(nil)
	if _, err := triggerWithSource(context.Background(), eng, "mer-1", domain.ReviewerClaudeCode, domain.AgentConfig{}, domain.ReviewTriggerAuto); err != nil {
		t.Fatal(err)
	}
	if launcher.gotSpec.Harness != domain.ReviewerCodex || startedFor(store, prB) != 1 || startedFor(store, prA) != 0 {
		t.Fatalf("harness %q, started A=%d B=%d; want codex reviewing B only", launcher.gotSpec.Harness, startedFor(store, prA), startedFor(store, prB))
	}
}

// With first-review-only, once Codex has reviewed B at its head, the next sweep at
// the same head must not hand B to Claude.
func TestNormalReviewerDoesNotRepeatStrongerFirstReview(t *testing.T) {
	eng, store, _ := switchEngine([]domain.ReviewRun{approved(domain.ReviewerClaudeCode, prA, "a"), approved(domain.ReviewerCodex, prB, "b")})
	eng.projects = fakeProjects{cfg: domain.ProjectConfig{ReviewRisk: domain.ReviewRiskConfig{Paths: riskPaths, Harness: domain.ReviewerCodex, Model: "gpt-6.1-sol", FirstReviewOnly: true}}}
	if _, err := triggerWithSource(context.Background(), eng, "mer-1", domain.ReviewerClaudeCode, domain.AgentConfig{}, domain.ReviewTriggerAuto); err != nil {
		t.Fatal(err)
	}
	if startedFor(store, prB) != 0 || startedFor(store, prA) != 0 {
		t.Fatalf("repeated a reviewed head: started A=%d B=%d", startedFor(store, prA), startedFor(store, prB))
	}
}

// A head that already has its stronger review cannot keep pulling the pass to
// Codex and starve the PRs that still need the normal reviewer (manual trigger,
// where reviewed heads are not dropped up front).
func TestReviewedRiskyHeadDoesNotStarveOtherPRs(t *testing.T) {
	eng, store, launcher := switchEngine([]domain.ReviewRun{approved(domain.ReviewerCodex, prB, "b")})
	if _, err := triggerWithSource(context.Background(), eng, "mer-1", "", domain.AgentConfig{}, domain.ReviewTriggerManual); err != nil {
		t.Fatal(err)
	}
	if launcher.gotSpec.Harness != domain.ReviewerClaudeCode || startedFor(store, prA) != 1 || startedFor(store, prB) != 0 {
		t.Fatalf("harness %q, started A=%d B=%d; want claude reviewing A only", launcher.gotSpec.Harness, startedFor(store, prA), startedFor(store, prB))
	}
}

// While Codex is still reviewing risky PR B, the next auto sweep must not start
// Claude on the same head.
func TestAutoSweepDoesNotDoubleAHeadRunningOnTheStrongerAgent(t *testing.T) {
	running := domain.ReviewRun{ID: "r-codex-b", ReviewID: "rv", SessionID: "mer-1", Harness: domain.ReviewerCodex, PRURL: prB, TargetSHA: "b", Status: domain.ReviewRunRunning}
	eng, store, _ := switchEngine([]domain.ReviewRun{approved(domain.ReviewerClaudeCode, prA, "a"), running})
	if _, err := triggerWithSource(context.Background(), eng, "mer-1", domain.ReviewerClaudeCode, domain.AgentConfig{}, domain.ReviewTriggerAuto); err != nil {
		t.Fatal(err)
	}
	if n := startedFor(store, prB); n != 1 { // only the seeded Codex run
		t.Fatalf("running runs on B = %d, want only the existing Codex run", n)
	}
	for _, r := range store.runs {
		if r.PRURL == prB && r.Harness == domain.ReviewerClaudeCode {
			t.Fatalf("claude started on a head codex is still reviewing: %+v", r)
		}
	}
}
