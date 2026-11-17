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

// approvedRetireGrace replaces the retire grace for a pane whose latest pass
// approved (B754): the worker's next step is a merge, not a fix for the
// reviewer to re-check, so keeping an idle agent for half an hour only holds
// its memory. A later re-review still relaunches a fresh pane.
const approvedRetireGrace = 5 * time.Minute

// idleReviewerLister is the optional store surface RetireIdleReviewers needs.
type idleReviewerLister interface {
	ListSessionsWithIdleTerminalReviewers(ctx stdctx.Context, updatedBefore time.Time) ([]domain.SessionID, error)
}

// RetireIdleReviewers destroys terminal reviewer panes whose last pass ended
// more than the retire grace ago (the shorter approved grace when that pass
// approved) and returns how many it reaped. Chat
// reviewers and archived ones are not touched; a pane with a running pass is
// never retired.
func (e *Engine) RetireIdleReviewers(ctx stdctx.Context) (int, error) {
	lister, ok := e.store.(idleReviewerLister)
	if !ok || e.retireGrace <= 0 {
		return 0, nil
	}
	now := e.clock()
	cutoffs := retireCutoffs{idle: now.Add(-e.retireGrace), approved: now.Add(-min(e.retireGrace, approvedRetireGrace))}
	ids, err := lister.ListSessionsWithIdleTerminalReviewers(ctx, cutoffs.approved)
	if err != nil {
		return 0, err
	}
	retired := 0
	for _, id := range ids {
		n, err := e.retireIdleReviewersFor(ctx, id, cutoffs)
		if err != nil {
			slog.Warn("review: could not retire an idle reviewer", "session", id, "err", err)
			continue
		}
		retired += n
	}
	return retired, nil
}

// retireCutoffs holds the instants before which a terminal reviewer row counts
// as idle: approved applies when the row's latest pass approved.
type retireCutoffs struct {
	idle, approved time.Time
}

// stale reports whether a row last touched at updated is past its cutoff.
func (c retireCutoffs) stale(updated time.Time, approvedLast bool) bool {
	if approvedLast {
		return updated.Before(c.approved)
	}
	return updated.Before(c.idle)
}

// approvedLastRuns names the reviews whose every PR's latest verdict approved.
// A review row carries a run per PR, and a batch inserts them with one
// timestamp, so the row's single newest run says nothing about its other PRs.
// Failed and cancelled runs carry no verdict and are skipped. runs come newest
// first (ListReviewRunsBySession).
func approvedLastRuns(runs []domain.ReviewRun) map[string]bool {
	type prKey struct{ review, pr string }
	seen := map[prKey]bool{}
	approved := map[string]bool{}
	for _, run := range runs {
		if run.Status != domain.ReviewRunComplete && run.Status != domain.ReviewRunDelivered {
			continue
		}
		key := prKey{run.ReviewID, run.PRURL}
		if seen[key] {
			continue
		}
		seen[key] = true
		ok, known := approved[run.ReviewID]
		approved[run.ReviewID] = (ok || !known) && run.Verdict == domain.VerdictApproved
	}
	return approved
}

func (e *Engine) retireIdleReviewersFor(ctx stdctx.Context, id domain.SessionID, cutoffs retireCutoffs) (int, error) {
	unlock, ok := e.tryLockWorker(id)
	if !ok {
		return 0, nil
	}
	defer unlock()
	reviews, err := e.store.ListReviewsBySession(ctx, id)
	if err != nil {
		return 0, err
	}
	runs, err := e.store.ListReviewRunsBySession(ctx, id)
	if err != nil {
		return 0, err
	}
	// Any running pass keeps the worker's pane: the handle is shared by every
	// harness row, so a stale row must not kill another row's live reviewer.
	// Every running row counts, even one that already recorded a verdict.
	for _, run := range runs {
		if run.Status == domain.ReviewRunRunning {
			return 0, nil
		}
	}
	approvedLast := approvedLastRuns(runs)
	// Decide per handle: destroy it only when every terminal row holding it is
	// past the cutoff; a stale row that shares it with a fresher row just loses
	// its handle.
	fresh := map[string]bool{}
	for _, review := range reviews {
		if review.InterfaceMode != domain.ReviewerInterfaceChat && !review.IsArchived && review.ReviewerHandleID != "" && !cutoffs.stale(review.UpdatedAt, approvedLast[review.ID]) {
			fresh[review.ReviewerHandleID] = true
		}
	}
	n := 0
	destroyed := map[string]bool{}
	for _, review := range reviews {
		if review.InterfaceMode == domain.ReviewerInterfaceChat || review.IsArchived || review.ReviewerHandleID == "" || !cutoffs.stale(review.UpdatedAt, approvedLast[review.ID]) {
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
