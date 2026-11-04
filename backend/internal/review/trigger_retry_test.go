package review

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// B356: an agent's --rerun on an already-reviewed head raced the sweep's retry
// of an earlier unsubmitted pass. The retry was decided before the rerun's run
// existed, then ran once the rerun released the worker lock, recording a
// second pass that replaced the reviewer mid-review and lost its verdict.
func TestRetryAfterRerunOnReviewedHeadStartsNoSecondPass(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteReviewStore(t)
	worker := liveWorker()
	seedReviewWorker(t, store, worker)
	now := time.Unix(1, 0).UTC()
	if err := store.UpsertReview(ctx, domain.Review{ID: "rev-claude-code", SessionID: worker.ID, ProjectID: worker.ProjectID, Harness: domain.ReviewerClaudeCode, InterfaceMode: domain.ReviewerInterfaceTUI, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("seed review: %v", err)
	}
	seed := func(run domain.ReviewRun, status domain.ReviewRunStatus, verdict domain.ReviewVerdict, body string) {
		t.Helper()
		if err := store.InsertReviewRun(ctx, run); err != nil {
			t.Fatalf("seed run %s: %v", run.ID, err)
		}
		if _, err := store.UpdateReviewRunResult(ctx, run.ID, status, verdict, body, "", true); err != nil {
			t.Fatalf("settle run %s: %v", run.ID, err)
		}
	}
	seed(requestedRun("run-approved", domain.ReviewerClaudeCode, domain.ReviewRunRunning, domain.VerdictNone, 1), domain.ReviewRunComplete, domain.VerdictApproved, "")
	unsubmitted := requestedRun("run-unsubmitted", domain.ReviewerClaudeCode, domain.ReviewRunRunning, domain.VerdictNone, 2)
	unsubmitted.TriggerSource = domain.ReviewTriggerAgent
	seed(unsubmitted, domain.ReviewRunFailed, domain.VerdictNone, "reviewer turn ended without submitting a result")

	launcher := &fakeLauncher{handle: "claude-pane"}
	eng := newEngineForTest(store, fakeSessions{rec: worker, ok: true}, prAt("sha1"), fakeProjects{}, launcher)
	rerun, err := eng.TriggerWithOptions(ctx, worker.ID, TriggerOptions{Harness: domain.ReviewerClaudeCode, Config: domain.AgentConfig{Model: "claude-sonnet-5-5"}, Source: domain.ReviewTriggerAgent, Rerun: true})
	if err != nil || len(rerun.CreatedRuns) != 1 {
		t.Fatalf("rerun = %+v, %v; want one new pass", rerun, err)
	}
	launcher.destroyed = false

	// The sweep's retry, decided before the rerun's run existed.
	retry, err := eng.TriggerWithOptions(ctx, worker.ID, TriggerOptions{Harness: domain.ReviewerClaudeCode, Config: domain.AgentConfig{Model: "claude-sonnet-5-5"}, Source: domain.ReviewTriggerAgent, PRURL: requestedPRURL, RetryOfRunID: unsubmitted.ID})
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if retry.Created || len(retry.CreatedRuns) != 0 || retry.SkipReason == "" {
		t.Fatalf("retry = %+v, want it skipped", retry)
	}
	if launcher.destroyed {
		t.Fatal("the retry tore down the reviewer working on the rerun")
	}
	got, ok, err := store.GetReviewRun(ctx, rerun.CreatedRuns[0].ID)
	if err != nil || !ok || got.Status != domain.ReviewRunRunning {
		t.Fatalf("rerun run = %+v ok=%v err=%v, want it still running", got, ok, err)
	}
	runs, err := store.ListReviewRunsBySession(ctx, worker.ID)
	if err != nil || len(runs) != 3 {
		t.Fatalf("runs = %d (%v), want approved + unsubmitted + one rerun", len(runs), err)
	}
}

// A retry still runs when nothing newer covers the commit.
func TestRetryOfUnsubmittedPassStartsWhenNothingNewer(t *testing.T) {
	failed := requestedRun("run-1", domain.ReviewerClaudeCode, domain.ReviewRunFailed, domain.VerdictNone, 2)
	older := requestedRun("run-0", domain.ReviewerClaudeCode, domain.ReviewRunComplete, domain.VerdictApproved, 1)
	other := requestedRun("run-x", domain.ReviewerCodex, domain.ReviewRunRunning, domain.VerdictNone, 3)
	if retryAlreadyCovered([]domain.ReviewRun{older, failed, other}, failed) {
		t.Fatal("an older pass or another reviewer's pass must not block the retry")
	}
	newer := requestedRun("run-2", domain.ReviewerClaudeCode, domain.ReviewRunFailed, domain.VerdictNone, 3)
	newer.Body = ReviewerInterruptedByRestart
	if retryAlreadyCovered([]domain.ReviewRun{failed, newer}, failed) {
		t.Fatal("a newer pass that never reached the commit must not block the retry")
	}
}

// A push seen while the retry waited on the lock: the retry must not review
// the new head, where it would replace that head's own pass.
func TestRetrySkippedWhenPRHeadMoved(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteReviewStore(t)
	worker := liveWorker()
	seedReviewWorker(t, store, worker)
	now := time.Unix(1, 0).UTC()
	if err := store.UpsertReview(ctx, domain.Review{ID: "rev-claude-code", SessionID: worker.ID, ProjectID: worker.ProjectID, Harness: domain.ReviewerClaudeCode, InterfaceMode: domain.ReviewerInterfaceTUI, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("seed review: %v", err)
	}
	failed := requestedRun("run-unsubmitted", domain.ReviewerClaudeCode, domain.ReviewRunRunning, domain.VerdictNone, 2)
	if err := store.InsertReviewRun(ctx, failed); err != nil {
		t.Fatalf("seed run: %v", err)
	}
	if _, err := store.UpdateReviewRunResult(ctx, failed.ID, domain.ReviewRunFailed, domain.VerdictNone, "reviewer turn ended without submitting a result", "", true); err != nil {
		t.Fatalf("fail run: %v", err)
	}
	launcher := &fakeLauncher{handle: "claude-pane"}
	eng := newEngineForTest(store, fakeSessions{rec: worker, ok: true}, prAt("sha2"), fakeProjects{}, launcher)
	res, err := eng.TriggerWithOptions(ctx, worker.ID, TriggerOptions{Harness: domain.ReviewerClaudeCode, Config: domain.AgentConfig{Model: "claude-sonnet-5-5"}, Source: domain.ReviewTriggerAgent, PRURL: requestedPRURL, RetryOfRunID: failed.ID})
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if res.Created || len(res.CreatedRuns) != 0 || res.SkipReason == "" || launcher.spawned {
		t.Fatalf("retry = %+v spawned=%v, want it skipped on a moved head", res, launcher.spawned)
	}
}
