package review

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// staleLauncher adds the optional AccountStale probe to the fake launcher.
type staleLauncher struct {
	*fakeLauncher
	stale bool
}

func (s staleLauncher) AccountStale(context.Context, string, domain.SessionID, domain.ReviewerHarness) bool {
	return s.stale
}

func triggerOnLiveReviewer(t *testing.T, runStatus domain.ReviewRunStatus, stale bool) *fakeLauncher {
	t.Helper()
	store := &fakeStore{
		review: &domain.Review{ID: "rev-1", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode, ReviewerHandleID: "review-mer-1", AgentSessionID: "native-reviewer-1"},
		runs:   []domain.ReviewRun{{ID: "run-0", SessionID: "mer-1", PRURL: "https://github.com/o/r/pull/1", TargetSHA: "sha0", Status: runStatus}},
	}
	fl := &fakeLauncher{alive: true, handle: "review-mer-1"}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, staleLauncher{fl, stale})
	if _, err := trigger(context.Background(), eng, "mer-1", "", domain.AgentConfig{}); err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	return fl
}

func TestTriggerReplacesIdleReviewerOnStaleAccount(t *testing.T) {
	fl := triggerOnLiveReviewer(t, domain.ReviewRunComplete, true)
	if !fl.destroyed || fl.destroyedHandle != "review-mer-1" || !fl.spawned || fl.notified {
		t.Fatalf("stale idle reviewer must be destroyed and respawned: %+v", fl)
	}
}

func TestTriggerKeepsIdleReviewerOnCurrentAccount(t *testing.T) {
	fl := triggerOnLiveReviewer(t, domain.ReviewRunComplete, false)
	if fl.destroyed || fl.spawned || !fl.notified {
		t.Fatalf("current-account reviewer must be notified in place: %+v", fl)
	}
}

func TestTriggerDoesNotReplaceMidReviewReviewer(t *testing.T) {
	fl := triggerOnLiveReviewer(t, domain.ReviewRunRunning, true)
	if fl.destroyed {
		t.Fatalf("a reviewer with a running run must not be destroyed: %+v", fl)
	}
}

// exitHookLauncher models the production supervisor: destroying a reviewer
// pane makes its supervisor post "exited" with its own launch id before the
// process is gone, and the store applies it only while the row still names
// that launch (UpdateReviewActivity's launch fence).
type exitHookLauncher struct {
	staleLauncher
	store *fakeStore
}

func (l exitHookLauncher) Destroy(ctx context.Context, handleID string) error {
	if row, ok := l.store.reviews[domain.ReviewerClaudeCode]; ok && row.ReviewerLaunchID == "launch-0" {
		row.ReviewerActivityState = domain.ActivityExited
		l.store.reviews[domain.ReviewerClaudeCode] = row
		l.store.review = &row
	}
	return l.fakeLauncher.Destroy(ctx, handleID)
}

// B364: one trigger, no earlier failure. The account-follow relaunch destroys
// the old pane while the row still carries its launch id, so the old
// supervisor's exit hook lands. The new launch must not inherit that state,
// or the next reconcile fails the fresh pass as "exited" and the sweep's
// retry starts a second pass on the same head.
func TestStaleAccountRelaunchKeepsNewPassRunning(t *testing.T) {
	store := &fakeStore{
		review: &domain.Review{ID: "rev-1", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode, ReviewerHandleID: "review-mer-1",
			AgentSessionID: "native-reviewer-1", ReviewerLaunchID: "launch-0", ReviewerActivityState: domain.ActivityIdle},
		runs: []domain.ReviewRun{{ID: "run-0", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode, PRURL: "https://github.com/o/r/pull/1", TargetSHA: "sha0", Status: domain.ReviewRunComplete}},
	}
	fl := &fakeLauncher{alive: true, handle: "review-mer-1"}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, exitHookLauncher{staleLauncher{fl, true}, store})
	res, err := trigger(context.Background(), eng, "mer-1", "", domain.AgentConfig{})
	if err != nil || len(res.CreatedRuns) != 1 || !fl.spawned {
		t.Fatalf("Trigger = %+v, %v; want one created run on a respawned reviewer", res, err)
	}
	if got := store.reviews[domain.ReviewerClaudeCode].ReviewerActivityState; got != domain.ActivityActive {
		t.Fatalf("new launch activity = %q, want active", got)
	}
	if _, err := eng.Reconcile(context.Background(), "mer-1"); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	for _, run := range store.runs {
		if run.ID == res.CreatedRuns[0].ID && run.Status != domain.ReviewRunRunning {
			t.Fatalf("fresh pass was settled by reconcile: %+v", run)
		}
	}
}
