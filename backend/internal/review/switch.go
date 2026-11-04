package review

import (
	stdctx "context"
	"fmt"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// SwitchReviewer serializes reviewer preference changes with trigger/restore
// and returns the authoritative post-switch review state.
func (e *Engine) SwitchReviewer(
	ctx stdctx.Context,
	workerID domain.SessionID,
	harness domain.ReviewerHarness,
	config domain.AgentConfig,
) (SessionReviews, error) {
	if workerID == "" {
		return SessionReviews{}, fmt.Errorf("%w: worker session id is required", ErrInvalid)
	}
	if harness != "" && !harness.IsKnown() {
		return SessionReviews{}, fmt.Errorf("%w: unknown reviewer harness %q", ErrInvalid, harness)
	}
	if err := config.Validate(); err != nil {
		return SessionReviews{}, fmt.Errorf("%w: reviewer config: %w", ErrInvalid, err)
	}
	if err := e.waitForChatRecovery(ctx); err != nil {
		return SessionReviews{}, err
	}
	unlock := e.lockWorker(workerID)
	defer unlock()

	worker, ok, err := e.sessions.GetSession(ctx, workerID)
	if err != nil {
		return SessionReviews{}, err
	}
	if !ok {
		return SessionReviews{}, fmt.Errorf("%w: worker session %q", ErrNotFound, workerID)
	}
	previousSelected, previousConfig, err := e.reviewerSelection(ctx, worker)
	if err != nil {
		return SessionReviews{}, err
	}
	if ok, err := e.store.SetSessionReviewerConfig(ctx, workerID, harness, config, e.clock()); err != nil {
		return SessionReviews{}, err
	} else if !ok {
		return SessionReviews{}, fmt.Errorf("%w: worker session %q", ErrNotFound, workerID)
	}
	worker.ReviewerHarness = harness
	worker.ReviewerConfig = config
	selected, selectedConfig, err := e.reviewerSelection(ctx, worker)
	if err != nil {
		return SessionReviews{}, err
	}
	reviewRows, err := e.store.ListReviewsBySession(ctx, workerID)
	if err != nil {
		return SessionReviews{}, err
	}
	if err := e.destroyOtherReviewerHandles(ctx, workerID, selected, reviewRows); err != nil {
		return SessionReviews{}, err
	}
	if previousSelected == selected && previousConfig != selectedConfig {
		if err := e.resetReviewerRuntimeLocked(ctx, workerID, selected); err != nil {
			return SessionReviews{}, err
		}
	}
	if _, err := e.restoreReviewerLocked(ctx, workerID, worker, selected, selectedConfig); err != nil {
		return SessionReviews{}, err
	}
	return e.listLocked(ctx, workerID, selected)
}

func reviewerPaneReusable(reviewRow domain.Review, hadRunningReviewer bool) bool {
	if strings.TrimSpace(reviewRow.AgentSessionID) != "" {
		return true
	}
	return hadRunningReviewer
}

func reviewRunsContainRunningForHarness(runs []domain.ReviewRun, harness domain.ReviewerHarness) bool {
	for _, run := range runs {
		if (run.Harness == harness || run.Harness == "") && run.Status == domain.ReviewRunRunning {
			return true
		}
	}
	return false
}

func (e *Engine) destroyOtherReviewerHandles(ctx stdctx.Context, workerID domain.SessionID, selected domain.ReviewerHarness, reviews []domain.Review) error {
	for _, review := range reviews {
		if review.Harness == selected || review.ReviewerHandleID == "" {
			continue
		}
		if err := e.launcher.Destroy(ctx, review.ReviewerHandleID); err != nil {
			return err
		}
		if err := e.store.ClearReviewerHandleByHarness(ctx, workerID, review.Harness); err != nil {
			return err
		}
		if _, err := e.store.CancelRunningReviewRunsBySessionAndHarness(ctx, workerID, review.Harness, "cancelled because reviewer agent was switched"); err != nil {
			return err
		}
	}
	return nil
}

// destroyIdleOtherReviewerHandles releases other reviewers' panes that have no
// running pass, while leaving any reviewer that is still working alone.
func (e *Engine) destroyIdleOtherReviewerHandles(ctx stdctx.Context, workerID domain.SessionID, selected domain.ReviewerHarness, reviews []domain.Review, runs []domain.ReviewRun) error {
	idle := make([]domain.Review, 0, len(reviews))
	for _, review := range reviews {
		if review.Harness != selected && !reviewRunsContainRunningForReview(runs, review) {
			idle = append(idle, review)
		}
	}
	return e.destroyOtherReviewerHandles(ctx, workerID, selected, idle)
}

func reviewRunsContainRunningForReview(runs []domain.ReviewRun, review domain.Review) bool {
	for _, run := range runs {
		if run.Status == domain.ReviewRunRunning && (run.ReviewID == review.ID || run.Harness == review.Harness) {
			return true
		}
	}
	return false
}

func (e *Engine) resetReviewerRuntimeLocked(ctx stdctx.Context, workerID domain.SessionID, harness domain.ReviewerHarness) error {
	review, ok, err := e.store.GetReviewBySessionAndHarness(ctx, workerID, harness)
	if err != nil || !ok {
		return err
	}
	if review.ReviewerHandleID != "" {
		if err := e.launcher.Destroy(ctx, review.ReviewerHandleID); err != nil {
			return err
		}
		if err := e.store.ClearReviewerHandleByHarness(ctx, workerID, harness); err != nil {
			return err
		}
	}
	if _, err := e.store.UpdateReviewAgentSessionID(ctx, review.ID, ""); err != nil {
		return err
	}
	return nil
}
