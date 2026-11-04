package review

import (
	stdctx "context"
	"log/slog"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// reviewerRetireGrace is how long a terminal reviewer pane may sit with no pass
// running before the sweep tears it down. The pane is kept after a pass so a
// quick re-review can reuse it; past this window it is only a leaked process
// tree (B493). A later trigger launches a fresh pane.
const reviewerRetireGrace = 30 * time.Minute

// idleReviewerLister is the optional store surface RetireIdleReviewers needs.
type idleReviewerLister interface {
	ListSessionsWithIdleTerminalReviewers(ctx stdctx.Context, updatedBefore time.Time) ([]domain.SessionID, error)
}

// RetireIdleReviewers destroys terminal reviewer panes whose last pass ended
// more than the retire grace ago and returns how many it reaped. Chat
// reviewers and archived ones are not touched; a pane with a running pass is
// never retired.
func (e *Engine) RetireIdleReviewers(ctx stdctx.Context) (int, error) {
	lister, ok := e.store.(idleReviewerLister)
	if !ok || e.retireGrace <= 0 {
		return 0, nil
	}
	cutoff := e.clock().Add(-e.retireGrace)
	ids, err := lister.ListSessionsWithIdleTerminalReviewers(ctx, cutoff)
	if err != nil {
		return 0, err
	}
	retired := 0
	for _, id := range ids {
		n, err := e.retireIdleReviewersFor(ctx, id, cutoff)
		if err != nil {
			slog.Warn("review: could not retire an idle reviewer", "session", id, "err", err)
			continue
		}
		retired += n
	}
	return retired, nil
}

func (e *Engine) retireIdleReviewersFor(ctx stdctx.Context, id domain.SessionID, cutoff time.Time) (int, error) {
	unlock, ok := e.tryLockWorker(id)
	if !ok {
		return 0, nil
	}
	defer unlock()
	reviews, err := e.store.ListReviewsBySession(ctx, id)
	if err != nil {
		return 0, err
	}
	running, err := e.store.ListRunningReviewRunsBySession(ctx, id)
	if err != nil {
		return 0, err
	}
	// Any running pass keeps the worker's pane: the handle is shared by every
	// harness row, so a stale row must not kill another row's live reviewer.
	if len(running) > 0 {
		return 0, nil
	}
	// Decide per handle: destroy it only when every terminal row holding it is
	// past the cutoff; a stale row that shares it with a fresher row just loses
	// its handle.
	fresh := map[string]bool{}
	for _, review := range reviews {
		if review.InterfaceMode != domain.ReviewerInterfaceChat && !review.IsArchived && review.ReviewerHandleID != "" && !review.UpdatedAt.Before(cutoff) {
			fresh[review.ReviewerHandleID] = true
		}
	}
	n := 0
	destroyed := map[string]bool{}
	for _, review := range reviews {
		if review.InterfaceMode == domain.ReviewerInterfaceChat || review.IsArchived || review.ReviewerHandleID == "" || !review.UpdatedAt.Before(cutoff) {
			continue
		}
		h := review.ReviewerHandleID
		if !fresh[h] && !destroyed[h] {
			if err := e.launcher.Destroy(ctx, h); err != nil {
				return n, err
			}
			destroyed[h] = true
		}
		if err := e.store.ClearReviewerHandleByHarness(ctx, id, review.Harness); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
