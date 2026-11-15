package review

import (
	stdctx "context"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// restorePendingLaunchID is stored in a terminal review row's launch id when
// worker teardown destroys its pane but keeps its running passes, so
// RestoreReviewer can resume them. The restore's own launch id replaces it; a
// reconcile treats a row carrying it as waiting for restore, not orphaned.
// Durable, so it survives the daemon restart between a shutdown save and the
// restore.
const restorePendingLaunchID = "restore-pending"

// markRestorePending flags each terminal reviewer that still has running
// passes after teardown cleared its handle.
func (e *Engine) markRestorePending(ctx stdctx.Context, workerID domain.SessionID, reviews []domain.Review) error {
	runs, err := e.store.ListReviewRunsBySession(ctx, workerID)
	if err != nil {
		return err
	}
	for _, review := range reviews {
		if review.InterfaceMode == domain.ReviewerInterfaceChat || len(runningRunsForReview(runs, review.ID)) == 0 {
			continue
		}
		review.ReviewerHandleID = ""
		review.ReviewerLaunchID = restorePendingLaunchID
		review.UpdatedAt = e.clock()
		if err := e.store.UpsertReview(ctx, review); err != nil {
			return err
		}
	}
	return nil
}

// failOrphanedRuns fails the running passes of a terminal reviewer that has no
// handle (B703): with no process they can never finish. Chat reviewers carry no
// pane by design, and a reviewer torn down for a worker save is waiting for
// RestoreReviewer to resume its passes.
func (e *Engine) failOrphanedRuns(ctx stdctx.Context, review domain.Review, running []domain.ReviewRun) (bool, error) {
	if review.InterfaceMode == domain.ReviewerInterfaceChat || review.ReviewerLaunchID == restorePendingLaunchID {
		return false, nil
	}
	for _, run := range running {
		if _, err := e.store.UpdateReviewRunResult(ctx, run.ID, domain.ReviewRunFailed, domain.VerdictNone, reviewerExitedBeforeSubmission, "", run.AutoInjectReview); err != nil {
			return false, err
		}
	}
	return true, nil
}

// settleRestorePending ends the wait of terminal reviewers still without a
// handle after a restore attempt on a live worker (the attempt may already have
// replaced the marker): the attempt is over, so a reviewer it did not relaunch
// (restore refused, failed before launch, or skipped) will
// never finish its passes, and they fail instead of hanging to the pass
// deadline. A worker still terminated keeps the marker; its restore is yet to
// come. Needs the worker lock.
func (e *Engine) settleRestorePending(ctx stdctx.Context, workerID domain.SessionID, cause error) error {
	worker, ok, err := e.sessions.GetSession(ctx, workerID)
	if err != nil || !ok || worker.IsTerminated {
		return err
	}
	reviews, err := e.store.ListReviewsBySession(ctx, workerID)
	if err != nil {
		return err
	}
	runs, err := e.store.ListReviewRunsBySession(ctx, workerID)
	if err != nil {
		return err
	}
	body := ReviewerRestoreFailedPrefix + "reviewer was not relaunched"
	if cause != nil {
		body = ReviewerRestoreFailedPrefix + cause.Error()
	}
	for _, review := range reviews {
		if review.InterfaceMode == domain.ReviewerInterfaceChat || review.ReviewerHandleID != "" {
			continue
		}
		if err := e.failRunningRestoredRuns(ctx, runningRunsForReview(runs, review.ID), body); err != nil {
			return err
		}
	}
	return nil
}
