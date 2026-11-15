package review

import (
	"context"
	"errors"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// B703: a terminal reviewer whose handle was cleared (worker teardown or an
// app restart) has no process; its running pass must fail, not stay running.
func TestListFailsRunningReviewWhenReviewerHandleCleared(t *testing.T) {
	store := &fakeStore{
		review: &domain.Review{ID: "rev-1", SessionID: "mer-1", Harness: domain.ReviewerKiro, ReviewerHandleID: "", ReviewerActivityState: domain.ActivityActive},
		runs: []domain.ReviewRun{{
			ID: "run-1", ReviewID: "rev-1", SessionID: "mer-1", Harness: domain.ReviewerKiro,
			PRURL: "https://github.com/o/r/pull/1", TargetSHA: "sha1", Status: domain.ReviewRunRunning,
		}},
	}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, &fakeLauncher{alive: false})

	got, err := eng.List(context.Background(), "mer-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got.Runs) != 1 || got.Runs[0].Status != domain.ReviewRunFailed || got.Runs[0].Body != reviewerExitedBeforeSubmission {
		t.Fatalf("runs = %+v, want failed reviewer-exit result", got.Runs)
	}
}

// Worker save tears the reviewer down but keeps its running pass. Even with the
// worker live again (after MarkSpawned, before RestoreReviewer) List must not
// fail it, and RestoreReviewer then resumes it.
func TestTeardownKeepsRunningReviewForRestore(t *testing.T) {
	store := &fakeStore{
		review: &domain.Review{ID: "rev-1", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode, ReviewerHandleID: "review-mer-1", AgentSessionID: "native-1", ReviewerActivityState: domain.ActivityActive},
		runs: []domain.ReviewRun{{
			ID: "run-1", ReviewID: "rev-1", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode,
			PRURL: "https://github.com/o/r/pull/1", TargetSHA: "sha1", Status: domain.ReviewRunRunning,
		}},
	}
	launcher := &fakeLauncher{alive: false, handle: "review-mer-1", reusable: true, reusableSet: true}
	worker := liveWorker()
	worker.ReviewerHarness = domain.ReviewerClaudeCode
	eng := newEngineForTest(store, fakeSessions{rec: worker, ok: true}, prAt("sha1"), fakeProjects{}, launcher)
	ctx := context.Background()

	if err := eng.TeardownReviewerTerminal(ctx, "mer-1"); err != nil {
		t.Fatalf("TeardownReviewerTerminal: %v", err)
	}
	got, err := eng.List(ctx, "mer-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got.Runs) != 1 || got.Runs[0].Status != domain.ReviewRunRunning {
		t.Fatalf("runs = %+v, want run kept running for restore", got.Runs)
	}
	if _, err := eng.RestoreReviewer(ctx, "mer-1"); err != nil {
		t.Fatalf("RestoreReviewer: %v", err)
	}
	if !launcher.restored || store.review.ReviewerHandleID == "" || store.review.ReviewerLaunchID == restorePendingLaunchID {
		t.Fatalf("review = %+v restored=%v, want relaunched reviewer with the marker consumed", store.review, launcher.restored)
	}
	if store.runs[0].Status != domain.ReviewRunRunning {
		t.Fatalf("run = %+v, want still running after restore", store.runs[0])
	}
}

// A restore of a live worker that ends without relaunching a restore-pending
// reviewer (here: its harness is being updated) must fail the pass, not leave
// it running with no process.
func TestFailedRestoreFailsRestorePendingRun(t *testing.T) {
	store := &fakeStore{
		review: &domain.Review{ID: "rev-1", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode, ReviewerHandleID: "review-mer-1", AgentSessionID: "native-1", ReviewerActivityState: domain.ActivityActive},
		runs: []domain.ReviewRun{{
			ID: "run-1", ReviewID: "rev-1", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode,
			PRURL: "https://github.com/o/r/pull/1", TargetSHA: "sha1", Status: domain.ReviewRunRunning,
		}},
	}
	launcher := &fakeLauncher{alive: false, handle: "review-mer-1", reusable: true, reusableSet: true}
	worker := liveWorker()
	worker.ReviewerHarness = domain.ReviewerClaudeCode
	eng := newEngineForTest(store, fakeSessions{rec: worker, ok: true}, prAt("sha1"), fakeProjects{}, launcher)
	eng.harnessUse = harnessGateFunc(func(domain.AgentHarness) (func(), bool) { return nil, false })
	ctx := context.Background()

	if err := eng.TeardownReviewerTerminal(ctx, "mer-1"); err != nil {
		t.Fatalf("TeardownReviewerTerminal: %v", err)
	}
	if _, err := eng.RestoreReviewer(ctx, "mer-1"); !errors.Is(err, ErrHarnessInstallActive) {
		t.Fatalf("RestoreReviewer error = %v, want ErrHarnessInstallActive", err)
	}
	if store.runs[0].Status != domain.ReviewRunFailed {
		t.Fatalf("run = %+v, want failed after the restore could not relaunch", store.runs[0])
	}
}
