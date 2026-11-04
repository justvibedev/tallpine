package review

import (
	stdctx "context"
	"fmt"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// List returns a worker's review state: the live reviewer handle and its passes.
func (e *Engine) List(ctx stdctx.Context, workerID domain.SessionID) (SessionReviews, error) {
	if workerID == "" {
		return SessionReviews{}, fmt.Errorf("%w: worker session id is required", ErrInvalid)
	}
	if err := e.waitForChatRecovery(ctx); err != nil {
		return SessionReviews{}, err
	}
	unlock := e.lockWorker(workerID)
	defer unlock()
	return e.listWorkerLocked(ctx, workerID)
}

// Reconcile settles a worker's running passes the way List does (exited
// reviewers, ended turns, the pass deadline) for the background sweep. It
// skips a worker whose trigger or restore is still in progress instead of
// queueing behind it; that operation reconciles on its own, and FailOverdueRuns
// covers one that hangs.
func (e *Engine) Reconcile(ctx stdctx.Context, workerID domain.SessionID) (bool, error) {
	if workerID == "" {
		return false, fmt.Errorf("%w: worker session id is required", ErrInvalid)
	}
	if err := e.waitForChatRecovery(ctx); err != nil {
		return false, err
	}
	unlock, ok := e.tryLockWorker(workerID)
	if !ok {
		return false, nil
	}
	defer unlock()
	_, err := e.listWorkerLocked(ctx, workerID)
	return err == nil, err
}

// FailOverdueRuns fails a worker's running passes that are past the pass
// deadline. It needs no runtime facts, so it neither waits for Chat recovery
// nor takes the worker lock: a launch or restore that hangs while holding that
// lock must not keep its pass running forever. The store only fails a pass
// that is still running, so a racing submit or trigger settles it once.
func (e *Engine) FailOverdueRuns(ctx stdctx.Context, workerID domain.SessionID) (bool, error) {
	if e.passDeadline <= 0 || workerID == "" {
		return false, nil
	}
	running, err := e.store.ListRunningReviewRunsBySession(ctx, workerID)
	if err != nil {
		return false, err
	}
	changed, _, err := e.failOverdueReviewerRuns(ctx, running)
	return changed, err
}

func (e *Engine) listWorkerLocked(ctx stdctx.Context, workerID domain.SessionID) (SessionReviews, error) {
	worker, ok, err := e.sessions.GetSession(ctx, workerID)
	if err != nil {
		return SessionReviews{}, err
	}
	if !ok {
		return SessionReviews{}, fmt.Errorf("%w: worker session %q", ErrNotFound, workerID)
	}
	selectedHarness, _, err := e.reviewerSelection(ctx, worker)
	if err != nil {
		return SessionReviews{}, err
	}
	return e.listLocked(ctx, workerID, selectedHarness)
}

func (e *Engine) listLocked(ctx stdctx.Context, workerID domain.SessionID, selectedHarness domain.ReviewerHarness) (SessionReviews, error) {
	runs, err := e.store.ListReviewRunsBySession(ctx, workerID)
	if err != nil {
		return SessionReviews{}, err
	}
	var reviewRow domain.Review
	reviewerHarness := selectedHarness
	if review, ok, err := e.store.GetReviewBySessionAndHarness(ctx, workerID, selectedHarness); err != nil {
		return SessionReviews{}, err
	} else if ok {
		reviewRow = review
		reviewerHarness = review.Harness
	} else if review, ok, err := e.store.GetReviewBySession(ctx, workerID); err != nil {
		return SessionReviews{}, err
	} else if ok {
		reviewRow = review
		reviewerHarness = review.Harness
	}
	if changed, err := e.reconcileExitedReviewer(ctx, &reviewRow, runs); err != nil {
		return SessionReviews{}, err
	} else if changed {
		runs, err = e.store.ListReviewRunsBySession(ctx, workerID)
		if err != nil {
			return SessionReviews{}, err
		}
	}
	reviewRows, err := e.store.ListReviewsBySession(ctx, workerID)
	if err != nil {
		return SessionReviews{}, err
	}
	active := []domain.ReviewerSurface{}
	if reviewerRowActive(reviewRow, runs) {
		active = append(active, reviewerSurface(reviewRow))
	}
	for i := range reviewRows {
		other := reviewRows[i]
		if other.ID == reviewRow.ID {
			continue
		}
		// Other reviewers can be running alongside the selected one; a pane that
		// died mid-pass must fail its runs the same way the selected one does.
		if changed, err := e.reconcileExitedReviewer(ctx, &other, runs); err != nil {
			return SessionReviews{}, err
		} else if changed {
			runs, err = e.store.ListReviewRunsBySession(ctx, workerID)
			if err != nil {
				return SessionReviews{}, err
			}
		}
		if reviewerRowActive(other, runs) {
			active = append(active, reviewerSurface(other))
		}
	}
	prs, err := e.prs.ListPRsBySession(ctx, workerID)
	if err != nil {
		return SessionReviews{}, err
	}
	worker, ok, err := e.sessions.GetSession(ctx, workerID)
	if err != nil {
		return SessionReviews{}, err
	}
	if ok {
		if prs, err = WithLocalTarget(ctx, worker, prs); err != nil {
			return SessionReviews{}, err
		}
	}
	return SessionReviews{
		ReviewerHandleID:      legacyReviewerHandle(reviewRow),
		ReviewerHarness:       reviewerHarness,
		ReviewerActivityState: reviewRow.ReviewerActivityState,
		Runs:                  runs,
		Reviews:               Plan(prs, runs),
		ReviewerSurface:       reviewerSurface(reviewRow),
		ActiveReviewers:       active,
	}, nil
}

// reviewerRowActive reports whether a reviewer has a pane to open or a pass
// still running. An archived reviewer is retired and never listed as active.
func reviewerRowActive(review domain.Review, runs []domain.ReviewRun) bool {
	if review.ID == "" || review.IsArchived {
		return false
	}
	if review.ReviewerHandleID != "" && review.ReviewerActivityState != domain.ActivityExited {
		return true
	}
	for _, run := range runs {
		if run.ReviewID == review.ID && run.Status == domain.ReviewRunRunning {
			return true
		}
	}
	return false
}

const (
	reviewerExitedBeforeSubmission = "reviewer process exited before submitting a result"
	reviewerIdleWithoutSubmission  = "reviewer turn ended without submitting a result"
	// ReviewerInterruptedByRestart is the body of a Chat pass whose turn a daemon
	// restart cut off.
	ReviewerInterruptedByRestart = "reviewer Chat turn interrupted by a restart"
	reviewerTrustDialogMarker    = "workspace trust dialog"

	reviewRunSupersededReason         = "superseded by a review trigger for a newer commit"
	reviewerChatUnavailableReason     = "cancelled because reviewer Chat is unavailable"
	reviewerTerminalUnavailableReason = "cancelled because reviewer terminal is unavailable"
)

// IsUnsubmittedReviewerFailure reports whether a failed pass's body says the
// reviewer died, went idle or ended its turn without submitting a result.
func IsUnsubmittedReviewerFailure(body string) bool {
	if strings.HasPrefix(body, ReviewerUsageLimitPrefix) || strings.HasPrefix(body, ReviewerAPIErrorPrefix) {
		return true
	}
	return body == reviewerExitedBeforeSubmission || body == reviewerIdleWithoutSubmission ||
		body == "reviewer Chat turn ended without submitting a result" ||
		body == ReviewerInterruptedByRestart ||
		strings.HasPrefix(body, ReviewerNotStartedPrefix)
}

// ReviewerUsageLimitPrefix starts the body of a pass whose reviewer turn died on
// the provider's usage limit; the rest is the provider's message.
const ReviewerUsageLimitPrefix = "reviewer usage limit: "

// UsageLimitPausedUntil reports when a pass that failed on the reviewer's usage
// limit may be retried. ok is false for any other run.
func UsageLimitPausedUntil(run domain.ReviewRun) (time.Time, bool) {
	if run.Status != domain.ReviewRunFailed || !strings.HasPrefix(run.Body, ReviewerUsageLimitPrefix) {
		return time.Time{}, false
	}
	failedAt := run.CreatedAt
	limit := domain.ParseConversationUsageLimit(strings.TrimPrefix(run.Body, ReviewerUsageLimitPrefix), &failedAt)
	if limit == nil {
		return time.Time{}, false
	}
	return limit.PausedUntil(failedAt), true
}

// EndedRunNeedsNotice reports whether a pass that ended without a verdict is
// news to the worker and its orchestrator: it failed, or Tallpine cancelled it
// because its reviewer became unavailable. A pass superseded by a newer
// commit's review, or cancelled by a person, a reviewer switch, or the
// worker leaving, ended because someone already moved on.
func EndedRunNeedsNotice(run domain.ReviewRun) bool {
	switch run.Status {
	case domain.ReviewRunFailed:
		return run.Body != reviewRunSupersededReason
	case domain.ReviewRunCancelled:
		return run.Body == reviewerChatUnavailableReason || run.Body == reviewerTerminalUnavailableReason
	}
	return false
}

// reviewerIdleGrace is the default for how long a live terminal reviewer may
// sit idle at its prompt while a pass is still running before that pass is
// failed (Deps.IdleGrace overrides it). A prompt
// queued behind the previous turn, or a Notify into an idle pane, flips the
// reviewer back to active within seconds; the margin also covers a turn that
// parks on a background task and is woken when it finishes.
const reviewerIdleGrace = 10 * time.Minute

// NeverReviewed reports whether a failed pass's body says it ended before the
// reviewer looked at the commit (usage limit, restart, trust dialog). Such a pass
// does not use up the commit's one retry.
func NeverReviewed(body string) bool {
	return strings.HasPrefix(body, ReviewerUsageLimitPrefix) || strings.HasPrefix(body, ReviewerAPIErrorPrefix) || body == ReviewerInterruptedByRestart ||
		(strings.HasPrefix(body, ReviewerNotStartedPrefix) && strings.Contains(body, reviewerTrustDialogMarker))
}
