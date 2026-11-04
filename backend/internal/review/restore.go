package review

import (
	stdctx "context"
	"errors"
	"fmt"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// RestoreReviewer relaunches the reviewer terminal for a restored worker when
// that worker already has review history. It does not create review_run rows or
// start a review; explicit trigger remains the only path that starts review
// work.
func (e *Engine) RestoreReviewer(ctx stdctx.Context, workerID domain.SessionID) (RestoreReviewerResult, error) {
	if workerID == "" {
		return RestoreReviewerResult{}, fmt.Errorf("%w: worker session id is required", ErrInvalid)
	}
	if err := e.waitForChatRecovery(ctx); err != nil {
		return RestoreReviewerResult{}, err
	}
	unlock := e.lockWorker(workerID)
	defer unlock()
	worker, ok, err := e.sessions.GetSession(ctx, workerID)
	if err != nil {
		return RestoreReviewerResult{}, err
	}
	if !ok {
		return RestoreReviewerResult{}, fmt.Errorf("%w: worker session %q", ErrNotFound, workerID)
	}
	if worker.IsTerminated || worker.Metadata.WorkspacePath == "" {
		return RestoreReviewerResult{}, nil
	}
	harness, config, err := e.reviewerSelection(ctx, worker)
	if err != nil {
		return RestoreReviewerResult{}, err
	}
	return e.restoreReviewerLocked(ctx, workerID, worker, harness, config)
}

// RecoverChatReviewers restores each durable chat reviewer by its persisted
// review row. It must not re-resolve the worker's current reviewer preference:
// that preference may have changed since this particular reviewer started.
func (e *Engine) RecoverChatReviewers(ctx stdctx.Context) error {
	reviews, err := e.store.ListRecoverableChatReviews(ctx)
	if err != nil {
		return err
	}
	var recoveryErrors []error
	for _, review := range reviews {
		if _, restoreErr := e.restoreRecoverableChatReviewer(ctx, review); restoreErr != nil {
			if _, recordErr := e.store.RecordReviewChatControllerError(ctx, review.ID, restoreErr.Error(), e.clock()); recordErr != nil {
				recoveryErrors = append(recoveryErrors, fmt.Errorf("record reviewer %s recovery error: %w", review.ID, recordErr))
			}
			recoveryErrors = append(recoveryErrors, fmt.Errorf("recover reviewer %s: %w", review.ID, restoreErr))
		}
	}
	return errors.Join(recoveryErrors...)
}

func (e *Engine) restoreRecoverableChatReviewer(ctx stdctx.Context, review domain.Review) (RestoreReviewerResult, error) {
	unlock := e.lockWorker(review.SessionID)
	defer unlock()
	current, exists, err := e.store.GetReviewBySessionAndHarness(ctx, review.SessionID, review.Harness)
	if err != nil {
		return RestoreReviewerResult{}, err
	}
	if !exists || current.ID != review.ID || current.IsArchived {
		return RestoreReviewerResult{}, nil
	}
	review = current
	worker, ok, err := e.sessions.GetSession(ctx, review.SessionID)
	if err != nil {
		return RestoreReviewerResult{}, err
	}
	if !ok {
		return RestoreReviewerResult{}, fmt.Errorf("%w: worker session %q", ErrNotFound, review.SessionID)
	}
	if worker.IsTerminated || worker.Metadata.WorkspacePath == "" {
		return RestoreReviewerResult{}, nil
	}
	runs, err := e.store.ListReviewRunsBySession(ctx, review.SessionID)
	if err != nil {
		return RestoreReviewerResult{}, err
	}
	return e.restorePersistedChatReviewerLocked(ctx, worker, review, reviewRunsForReview(runs, review.ID))
}

func (e *Engine) restorePersistedChatReviewerLocked(ctx stdctx.Context, worker domain.SessionRecord, review domain.Review, previousRuns []domain.ReviewRun) (RestoreReviewerResult, error) {
	if review.ReviewerHandleID != "" {
		alive, err := e.launcher.Alive(ctx, review.ReviewerHandleID, review.ReviewerLaunchID)
		if err != nil {
			return RestoreReviewerResult{}, err
		}
		if alive {
			return RestoreReviewerResult{ReviewerHandleID: review.ReviewerHandleID}, nil
		}
	}
	if review.InterfaceMode == domain.ReviewerInterfaceChat && e.launcher.InterfaceMode(review.Harness) != domain.ReviewerInterfaceChat {
		if err := e.stopReviewChatWork(ctx, review); err != nil {
			return RestoreReviewerResult{}, err
		}
		if _, err := e.store.CancelRunningReviewRunsBySessionAndHarness(ctx, worker.ID, review.Harness, reviewerChatUnavailableReason); err != nil {
			return RestoreReviewerResult{}, err
		}
		if err := e.setReviewerInterfaceMode(ctx, review.ID, domain.ReviewerInterfaceTUI, e.clock()); err != nil {
			return RestoreReviewerResult{}, err
		}
		if _, err := e.store.UpdateReviewAgentSessionID(ctx, review.ID, ""); err != nil {
			return RestoreReviewerResult{}, err
		}
		review.InterfaceMode = domain.ReviewerInterfaceTUI
		review.AgentSessionID = ""
		review.ProviderConversationID = ""
	}
	launchID := e.newID()
	projectEnv, err := e.projectEnv(ctx, worker.ProjectID, review.Harness)
	if err != nil {
		return RestoreReviewerResult{}, err
	}
	releaseHarness, err := e.beginHarnessUse(review.Harness)
	if err != nil {
		return RestoreReviewerResult{}, err
	}
	defer releaseHarness()
	launch, err := e.launcher.RestoreTerminal(ctx, LaunchSpec{ReviewSessionID: review.ID, LaunchID: launchID, PreviousHandleID: review.ReviewerHandleID, WorkerID: worker.ID, ProjectID: worker.ProjectID, ProjectEnv: projectEnv, Harness: review.Harness, WorkspacePath: worker.Metadata.WorkspacePath, AgentSessionID: review.AgentSessionID, ProviderConversationID: review.ProviderConversationID, PreviousRuns: previousRuns, InterfaceMode: review.InterfaceMode})
	if err != nil {
		return RestoreReviewerResult{}, fmt.Errorf("restore reviewer: %w", err)
	}
	agentSessionID := review.AgentSessionID
	if launch.AgentSessionID != "" {
		agentSessionID = launch.AgentSessionID
	}
	if _, err := e.upsertReview(ctx, worker, review.Harness, launch.HandleID, agentSessionID, launch.LaunchID, "", e.clock()); err != nil {
		_ = e.launcher.Destroy(ctx, launch.HandleID)
		return RestoreReviewerResult{}, err
	}
	return RestoreReviewerResult{ReviewerHandleID: launch.HandleID, Restored: true}, nil
}

func (e *Engine) restoreReviewerLocked(
	ctx stdctx.Context,
	workerID domain.SessionID,
	worker domain.SessionRecord,
	harness domain.ReviewerHarness,
	config domain.AgentConfig,
) (RestoreReviewerResult, error) {
	reviewRows, err := e.store.ListReviewsBySession(ctx, workerID)
	if err != nil {
		return RestoreReviewerResult{}, err
	}
	runs, err := e.store.ListReviewRunsBySession(ctx, workerID)
	if err != nil {
		return RestoreReviewerResult{}, err
	}
	// Restoring the selected reviewer must not cancel another reviewer that is
	// still running alongside it; only idle panes of other reviewers go. One
	// whose pane did not survive cannot finish, so its passes are cancelled
	// rather than left looking like a review still in progress.
	if err := e.destroyIdleOtherReviewerHandles(ctx, workerID, harness, reviewRows, runs); err != nil {
		return RestoreReviewerResult{}, err
	}
	for _, other := range reviewRows {
		if other.Harness == harness || !reviewRunsContainRunningForReview(runs, other) {
			continue
		}
		// A Chat reviewer has no pane by design; RecoverChatReviewers restores
		// its durable conversation, so a missing handle is not a dead reviewer.
		if other.InterfaceMode == domain.ReviewerInterfaceChat {
			continue
		}
		if _, err := e.cancelStaleRunningRuns(ctx, workerID, other.Harness, other, true, runs); err != nil {
			return RestoreReviewerResult{}, err
		}
	}
	reviewRow, hasReview, err := e.store.GetReviewBySessionAndHarness(ctx, workerID, harness)
	if err != nil {
		return RestoreReviewerResult{}, err
	}
	if hasReview && reviewRow.IsArchived {
		return RestoreReviewerResult{}, nil
	}
	previousRuns := reviewRunsForHarness(runs, harness)
	if !hasReview && len(previousRuns) == 0 {
		return RestoreReviewerResult{}, nil
	}
	if hasReview && reviewRow.ReviewerHandleID != "" {
		alive, err := e.launcher.Alive(ctx, reviewRow.ReviewerHandleID, reviewRow.ReviewerLaunchID)
		if err != nil {
			return RestoreReviewerResult{}, err
		}
		if alive {
			return RestoreReviewerResult{ReviewerHandleID: reviewRow.ReviewerHandleID, Restored: false}, nil
		}
	}
	// A one-shot reviewer (ReviewerReusePolicy false) cannot start idle: its
	// process only exists while it runs a task. A dead one has nothing to
	// restore; its unfinished passes cannot complete, and the next trigger
	// spawns a fresh process.
	if !e.launcher.Reusable(harness) {
		if err := e.failRunningRestoredRuns(ctx, previousRuns, "reviewer process ended and cannot be restored idle; retry the review"); err != nil {
			return RestoreReviewerResult{}, err
		}
		return RestoreReviewerResult{}, nil
	}
	agentSessionID := ""
	if hasReview {
		agentSessionID = reviewRow.AgentSessionID
	} else {
		reviewRow, err = e.upsertReview(ctx, worker, harness, "", "", "", domain.ActivityIdle, e.clock())
		if err != nil {
			return RestoreReviewerResult{}, err
		}
	}
	launchID := e.newID()
	// A reviewer being relaunched is no longer "exited". Clear that before the
	// launch so clients stop offering Restore for a reviewer that is back, while
	// state the new process reports through its hooks during launch still wins
	// (the finalizing upsert below preserves it).
	restoredActivity := domain.ActivityState("")
	wasExited := reviewRow.ReviewerActivityState == domain.ActivityExited
	if wasExited {
		restoredActivity = domain.ActivityIdle
	}
	reviewRow, err = e.upsertReview(ctx, worker, harness, reviewRow.ReviewerHandleID, agentSessionID, launchID, restoredActivity, e.clock())
	if err != nil {
		return RestoreReviewerResult{}, err
	}
	if !hasReview {
		reviewRow.InterfaceMode = e.launcher.InterfaceMode(harness)
		if err := e.setReviewerInterfaceMode(ctx, reviewRow.ID, reviewRow.InterfaceMode, e.clock()); err != nil {
			return RestoreReviewerResult{}, err
		}
	} else if reviewRow.InterfaceMode == domain.ReviewerInterfaceChat && e.launcher.InterfaceMode(harness) != domain.ReviewerInterfaceChat {
		if err := e.stopReviewChatWork(ctx, reviewRow); err != nil {
			return RestoreReviewerResult{}, err
		}
		if _, err := e.store.CancelRunningReviewRunsBySessionAndHarness(ctx, worker.ID, harness, reviewerChatUnavailableReason); err != nil {
			return RestoreReviewerResult{}, err
		}
		if err := e.setReviewerInterfaceMode(ctx, reviewRow.ID, domain.ReviewerInterfaceTUI, e.clock()); err != nil {
			return RestoreReviewerResult{}, err
		}
		if _, err := e.store.UpdateReviewAgentSessionID(ctx, reviewRow.ID, ""); err != nil {
			return RestoreReviewerResult{}, err
		}
		reviewRow.InterfaceMode = domain.ReviewerInterfaceTUI
		reviewRow.AgentSessionID = ""
		agentSessionID = ""
	}
	projectEnv, err := e.projectEnv(ctx, worker.ProjectID, harness)
	if err != nil {
		return RestoreReviewerResult{}, err
	}
	releaseHarness, err := e.beginHarnessUse(harness)
	if err != nil {
		return RestoreReviewerResult{}, err
	}
	defer releaseHarness()
	launch, err := e.launcher.RestoreTerminal(ctx, LaunchSpec{
		ReviewSessionID:      reviewRow.ID,
		LaunchID:             launchID,
		PreviousHandleID:     reviewRow.ReviewerHandleID,
		WorkerID:             worker.ID,
		ProjectID:            worker.ProjectID,
		ProjectEnv:           projectEnv,
		Harness:              harness,
		AgentConfig:          config,
		WorkspacePath:        worker.Metadata.WorkspacePath,
		AgentSessionID:       agentSessionID,
		RequireNativeHistory: harness == domain.ReviewerCodex && strings.TrimSpace(agentSessionID) != "",
		PreviousRuns:         previousRuns,
		InterfaceMode:        reviewRow.InterfaceMode,
	})
	if err != nil {
		restoreErr := fmt.Errorf("restore reviewer: %w", err)
		if wasExited {
			// The relaunch failed, so the reviewer is still gone.
			if _, markErr := e.upsertReview(ctx, worker, harness, reviewRow.ReviewerHandleID, agentSessionID, launchID, domain.ActivityExited, e.clock()); markErr != nil {
				restoreErr = errors.Join(restoreErr, markErr)
			}
		}
		if failErr := e.failRunningRestoredRuns(ctx, previousRuns, restoreErr.Error()); failErr != nil {
			return RestoreReviewerResult{}, errors.Join(restoreErr, failErr)
		}
		return RestoreReviewerResult{}, restoreErr
	}
	if !launch.NativeResumed {
		const reason = "reviewer native conversation was unavailable during restore; retry the review"
		if err := e.failRunningRestoredRuns(ctx, previousRuns, reason); err != nil {
			_ = e.launcher.Destroy(ctx, launch.HandleID)
			return RestoreReviewerResult{}, err
		}
	}
	if launch.AgentSessionID != "" {
		agentSessionID = launch.AgentSessionID
	}
	if _, err := e.upsertReview(ctx, worker, harness, launch.HandleID, agentSessionID, launch.LaunchID, "", e.clock()); err != nil {
		_ = e.launcher.Destroy(ctx, launch.HandleID)
		return RestoreReviewerResult{}, err
	}
	return RestoreReviewerResult{ReviewerHandleID: launch.HandleID, Restored: true}, nil
}

func (e *Engine) failRunningRestoredRuns(ctx stdctx.Context, runs []domain.ReviewRun, body string) error {
	for _, run := range runs {
		if run.Status != domain.ReviewRunRunning {
			continue
		}
		if _, err := e.store.UpdateReviewRunResult(ctx, run.ID, domain.ReviewRunFailed, domain.VerdictNone, body, "", run.AutoInjectReview); err != nil {
			return fmt.Errorf("fail review run %q after reviewer restore: %w", run.ID, err)
		}
	}
	return nil
}

// TeardownReviewerTerminal destroys reviewer panes before shutdown removes the
// worker worktree, but preserves review rows and native agent session ids so
// worker restore can recreate the reviewer terminal later.
func (e *Engine) TeardownReviewerTerminal(ctx stdctx.Context, workerID domain.SessionID) error {
	if workerID == "" {
		return fmt.Errorf("%w: worker session id is required", ErrInvalid)
	}
	if err := e.waitForChatRecovery(ctx); err != nil {
		return err
	}
	unlock := e.lockWorker(workerID)
	defer unlock()
	reviews, err := e.store.ListReviewsBySession(ctx, workerID)
	if err != nil {
		return err
	}
	for _, review := range reviews {
		if review.ReviewerHandleID == "" {
			continue
		}
		if err := e.launcher.Destroy(ctx, review.ReviewerHandleID); err != nil {
			return err
		}
	}
	if len(reviews) > 0 {
		if err := e.store.ClearReviewerHandle(ctx, workerID); err != nil {
			return err
		}
	}
	return nil
}
