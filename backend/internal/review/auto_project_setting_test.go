package review

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// B389: the project setting covers a worker whose own flag was copied as off.
func TestAutoTriggerFollowsProjectAutoReviewSetting(t *testing.T) {
	on := true
	worker := liveWorker()
	worker.AutoReviewEnabled = false
	store := &fakeStore{}
	launcher := &fakeLauncher{handle: "review-mer-1"}
	eng := newEngineForTest(store, fakeSessions{rec: worker, ok: true}, prAt("sha1"), fakeProjects{cfg: domain.ProjectConfig{AutoReview: &on}}, launcher)
	result, err := triggerWithSource(context.Background(), eng, "mer-1", domain.ReviewerClaudeCode, domain.AgentConfig{}, domain.ReviewTriggerAuto)
	if err != nil || !result.Created || !launcher.spawned {
		t.Fatalf("result=%+v err=%v spawned=%v", result, err, launcher.spawned)
	}
}

type overrideProjects struct {
	fakeProjects
	enabled bool
}

func (o overrideProjects) SessionAutoReviewOverride(context.Context, domain.SessionID) (bool, bool, error) {
	return o.enabled, true, nil
}

func TestAutoTriggerSessionOverrideBeatsProjectSetting(t *testing.T) {
	on := true
	store := &fakeStore{}
	launcher := &fakeLauncher{handle: "review-mer-1"}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), overrideProjects{fakeProjects{cfg: domain.ProjectConfig{AutoReview: &on}}, false}, launcher)
	result, err := triggerWithSource(context.Background(), eng, "mer-1", domain.ReviewerClaudeCode, domain.AgentConfig{}, domain.ReviewTriggerAuto)
	if err != nil || result.Created || result.SkipReason != "disabled" || launcher.spawned {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

// B546: the risk pick may name a stronger reviewer than the approver; the
// carried approval must satisfy the plan for that reviewer, so an auto pass
// launches nothing and a repeat pass adds no second carried row.
func TestAutoCarryAcrossReviewersLaunchesNothingAndIsIdempotent(t *testing.T) {
	const url = "https://github.com/o/r/pull/1"
	prior := domain.ReviewRun{ID: "r-old", ReviewID: "rv1", SessionID: "mer-1", Harness: domain.ReviewerCodex, PRURL: url, TargetSHA: "old",
		Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved, CreatedAt: time.Now().Add(-time.Hour)}
	store := &fakeStore{runs: []domain.ReviewRun{prior}}
	launcher := &fakeLauncher{handle: "review-mer-1"}
	prs := fakePRs{prs: []domain.PullRequest{{URL: url, Number: 1, HeadSHA: "sha1", TargetBranch: "main"}}}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prs, fakeProjects{cfg: domain.ProjectConfig{ApproveIdenticalPatch: true}}, launcher)
	eng.changedPaths = func(context.Context, string, domain.PullRequest) ([]string, error) { return nil, nil }
	eng.patchID = func(context.Context, string, string, string) (string, error) { return "p", nil }
	for i := 0; i < 2; i++ {
		if _, err := triggerWithSource(context.Background(), eng, "mer-1", domain.ReviewerClaudeCode, domain.AgentConfig{}, domain.ReviewTriggerAuto); err != nil {
			t.Fatal(err)
		}
	}
	carried := 0
	for _, r := range store.runs {
		if r.TargetSHA == "sha1" {
			carried++
		}
	}
	if launcher.spawned || carried != 1 {
		t.Fatalf("spawned=%v carried rows=%d, want none launched and one carried row", launcher.spawned, carried)
	}
}

type fakeProjects struct{ cfg domain.ProjectConfig }

// GetProject turns Auto review on unless the test sets it: the project setting
// decides Auto review for a live worker.
func (f fakeProjects) GetProject(_ context.Context, id string) (domain.ProjectRecord, bool, error) {
	cfg := f.cfg
	if cfg.AutoReview == nil {
		on := true
		cfg.AutoReview = &on
	}
	return domain.ProjectRecord{ID: id, Config: cfg}, true, nil
}

func autoOffProjects() fakeProjects {
	off := false
	return fakeProjects{cfg: domain.ProjectConfig{AutoReview: &off}}
}
