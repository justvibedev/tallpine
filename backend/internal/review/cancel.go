package review

import (
	stdctx "context"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// Cancel interrupts the live reviewer pane for a worker and marks running
// review runs as cancelled so they no longer block a fresh trigger.
func (e *Engine) Cancel(ctx stdctx.Context, workerID domain.SessionID) (CancelResult, error) {
	if workerID == "" {
		return CancelResult{}, fmt.Errorf("%w: worker session id is required", ErrInvalid)
	}
	if err := e.waitForChatRecovery(ctx); err != nil {
		return CancelResult{}, err
	}
	worker, ok, err := e.sessions.GetSession(ctx, workerID)
	if err != nil {
		return CancelResult{}, err
	}
	if !ok {
		return CancelResult{}, fmt.Errorf("%w: worker session %q", ErrNotFound, workerID)
	}
	harness, _, err := e.reviewerSelection(ctx, worker)
	if err != nil {
		return CancelResult{}, err
	}
	running, err := e.store.ListRunningReviewRunsBySession(ctx, workerID)
	if err != nil {
		return CancelResult{}, err
	}
	if len(running) == 0 {
		review, ok, err := e.currentReviewForSession(ctx, workerID, harness)
		if err != nil {
			return CancelResult{}, err
		}
		handle := ""
		if ok {
			handle = review.ReviewerHandleID
		}
		prs, err := e.prs.ListPRsBySession(ctx, workerID)
		if err != nil {
			return CancelResult{}, err
		}
		prs, err = WithLocalTarget(ctx, worker, prs)
		if err != nil {
			return CancelResult{}, err
		}
		runs, err := e.store.ListReviewRunsBySession(ctx, workerID)
		if err != nil {
			return CancelResult{}, err
		}
		return CancelResult{ReviewerHandleID: handle, Reviews: Plan(prs, runs)}, nil
	}
	// Several reviewers may be running on this worker at once; cancel stops
	// all of them. The selected reviewer is cancelled first and its handle is
	// the one reported, so single-reviewer clients see what they did before.
	reviewers, err := e.runningReviewersForCancel(ctx, workerID, harness, running)
	if err != nil {
		return CancelResult{}, err
	}
	if len(reviewers) == 0 {
		return CancelResult{}, fmt.Errorf("%w: reviewer for worker session %q", ErrNotFound, workerID)
	}
	cancelledHarness := make(map[domain.ReviewerHarness]bool, len(reviewers))
	for _, review := range reviewers {
		if err := e.launcher.Cancel(ctx, review.ReviewerHandleID, review.Harness); err != nil {
			alive, aliveErr := e.launcher.Alive(ctx, review.ReviewerHandleID, review.ReviewerLaunchID)
			if aliveErr != nil {
				return CancelResult{}, err
			}
			if alive {
				return CancelResult{}, err
			}
		}
		if _, err := e.store.CancelRunningReviewRunsBySessionAndHarness(ctx, workerID, review.Harness, "cancelled by user"); err != nil {
			return CancelResult{}, err
		}
		cancelledHarness[review.Harness] = true
	}
	primary := reviewers[0]
	// Legacy rows have no harness; they belong to the reported reviewer.
	hasLegacy := false
	for _, run := range running {
		hasLegacy = hasLegacy || run.Harness == ""
	}
	if hasLegacy {
		if _, err := e.store.CancelRunningReviewRunsBySessionAndHarness(ctx, workerID, "", "cancelled by user"); err != nil {
			return CancelResult{}, err
		}
	}
	cancelled := make([]domain.ReviewRun, 0, len(running))
	for _, run := range running {
		if run.Harness != "" && !cancelledHarness[run.Harness] {
			continue
		}
		run.Status = domain.ReviewRunCancelled
		run.Verdict = domain.VerdictNone
		run.Body = "cancelled by user"
		run.GithubReviewID = ""
		cancelled = append(cancelled, run)
	}
	prs, err := e.prs.ListPRsBySession(ctx, workerID)
	if err != nil {
		return CancelResult{}, err
	}
	prs, err = WithLocalTarget(ctx, worker, prs)
	if err != nil {
		return CancelResult{}, err
	}
	runs, err := e.store.ListReviewRunsBySession(ctx, workerID)
	if err != nil {
		return CancelResult{}, err
	}
	return CancelResult{ReviewerHandleID: primary.ReviewerHandleID, Reviews: Plan(prs, runs), CancelledRuns: cancelled}, nil
}

// runningReviewersForCancel returns every reviewer row with a live handle that
// owns a running pass, selected reviewer first. Legacy runs with no harness
// resolve to the selected reviewer, matching currentReviewForCancel.
func (e *Engine) runningReviewersForCancel(ctx stdctx.Context, workerID domain.SessionID, selected domain.ReviewerHarness, running []domain.ReviewRun) ([]domain.Review, error) {
	var out []domain.Review
	seen := map[domain.ReviewerHarness]bool{}
	add := func(review domain.Review) {
		if review.ReviewerHandleID == "" || seen[review.Harness] {
			return
		}
		seen[review.Harness] = true
		out = append(out, review)
	}
	primary, ok, err := e.currentReviewForCancel(ctx, workerID, selected, running)
	if err != nil {
		return nil, err
	}
	if ok {
		add(primary)
	}
	for _, run := range running {
		if run.Harness == "" || seen[run.Harness] {
			continue
		}
		review, ok, err := e.store.GetReviewBySessionAndHarness(ctx, workerID, run.Harness)
		if err != nil {
			return nil, err
		}
		if ok {
			add(review)
		}
	}
	return out, nil
}

func (e *Engine) currentReviewForCancel(ctx stdctx.Context, workerID domain.SessionID, selected domain.ReviewerHarness, running []domain.ReviewRun) (domain.Review, bool, error) {
	if len(running) > 0 {
		if selected != "" {
			for _, run := range running {
				if run.Harness != selected && run.Harness != "" {
					continue
				}
				review, ok, err := e.store.GetReviewBySessionAndHarness(ctx, workerID, selected)
				if err != nil || (ok && review.ReviewerHandleID != "") {
					return review, ok, err
				}
				break
			}
		}
		for _, run := range running {
			if run.Harness == "" || run.Harness == selected {
				continue
			}
			review, ok, err := e.store.GetReviewBySessionAndHarness(ctx, workerID, run.Harness)
			if err != nil || (ok && review.ReviewerHandleID != "") {
				return review, ok, err
			}
		}
	}
	return e.currentReviewForSession(ctx, workerID, selected)
}

func (e *Engine) currentReviewForSession(ctx stdctx.Context, workerID domain.SessionID, selected domain.ReviewerHarness) (domain.Review, bool, error) {
	if selected != "" {
		review, ok, err := e.store.GetReviewBySessionAndHarness(ctx, workerID, selected)
		if err != nil || ok {
			return review, ok, err
		}
	}
	return e.store.GetReviewBySession(ctx, workerID)
}

// TerminateReviewer destroys the live reviewer pane for a worker and cancels
// any running review runs. Unlike Cancel, this does not ask the reviewer
// adapter for a graceful interrupt sequence: worker termination/restore must
// remove the terminal pane itself.
func (e *Engine) TerminateReviewer(ctx stdctx.Context, workerID domain.SessionID, body string) (TerminateResult, error) {
	return e.terminateReviewer(ctx, workerID, body, false)
}

// ArchiveReviewer retires all reviewer surfaces but retains conversations and results.
func (e *Engine) ArchiveReviewer(ctx stdctx.Context, workerID domain.SessionID) error {
	_, err := e.terminateReviewer(ctx, workerID, "cancelled because reviewer was archived", true)
	return err
}

func (e *Engine) terminateReviewer(ctx stdctx.Context, workerID domain.SessionID, body string, archive bool) (TerminateResult, error) {
	if workerID == "" {
		return TerminateResult{}, fmt.Errorf("%w: worker session id is required", ErrInvalid)
	}
	if err := e.waitForChatRecovery(ctx); err != nil {
		return TerminateResult{}, err
	}
	unlock := e.lockWorker(workerID)
	defer unlock()
	reviews, err := e.store.ListReviewsBySession(ctx, workerID)
	if err != nil {
		return TerminateResult{}, err
	}
	running, err := e.store.ListRunningReviewRunsBySession(ctx, workerID)
	if err != nil {
		return TerminateResult{}, err
	}
	destroyedHandle := ""
	for _, review := range reviews {
		if archive && review.InterfaceMode == domain.ReviewerInterfaceChat {
			if err := e.stopReviewChatWork(ctx, review); err != nil {
				return TerminateResult{}, err
			}
		} else if review.ReviewerHandleID != "" {
			if err := e.launcher.Destroy(ctx, review.ReviewerHandleID); err != nil {
				return TerminateResult{}, err
			}
		}
		if destroyedHandle == "" {
			destroyedHandle = review.ReviewerHandleID
		}
	}
	if !archive && len(reviews) > 0 {
		if err := e.store.ClearReviewerHandle(ctx, workerID); err != nil {
			return TerminateResult{}, err
		}
	}
	if body == "" {
		body = "cancelled by worker session lifecycle"
	}
	if _, err := e.store.CancelRunningReviewRunsBySession(ctx, workerID, body); err != nil {
		return TerminateResult{}, err
	}
	if archive {
		if err := e.store.ArchiveReviewsBySession(ctx, workerID, e.clock()); err != nil {
			return TerminateResult{}, err
		}
		return TerminateResult{}, nil
	}
	cancelled := make([]domain.ReviewRun, 0, len(running))
	for _, run := range running {
		run.Status = domain.ReviewRunCancelled
		run.Verdict = domain.VerdictNone
		run.Body = body
		run.GithubReviewID = ""
		cancelled = append(cancelled, run)
	}
	return TerminateResult{ReviewerHandleID: destroyedHandle, CancelledRuns: cancelled}, nil
}
