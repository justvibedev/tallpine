package scm

import "tallpine.eu/tallpine/backend/internal/ports"

// repoRefreshTracker records, for one poll, which repos refreshed completely so
// the repo ETag/cursor only advance when nothing was skipped or lost.
type repoRefreshTracker struct {
	guards map[string]repoGuardState
	ok     map[string]bool
	// listFailed records repos whose PR listing (ListPRsByRepo) failed
	// during this poll. markOK checks this set and refuses to
	// flip a repo back to OK when the listing itself failed — without this,
	// a successful PR write for a different PR in the same repo would clear
	// the listing failure and advance the ETag/cursor, making the failed
	// listing unrecoverable on the next poll.
	listFailed map[string]bool
}

func newRepoRefreshTracker(guards map[string]repoGuardState) *repoRefreshTracker {
	return &repoRefreshTracker{guards: guards, ok: pendingRepoRefreshes(guards), listFailed: map[string]bool{}}
}

// markFailed marks a repo refresh-incomplete.
func (r *repoRefreshTracker) markFailed(repo ports.SCMRepo) {
	key := prKey(repo, 0)
	if _, ok := r.ok[key]; ok {
		r.ok[key] = false
	}
}

// markListFailed marks incomplete discovery (a failed listing or missing
// human identity). It sets listFailed so
// markOK cannot clear it within the same poll, and also
// marks the repo refresh-incomplete via markFailed.
func (r *repoRefreshTracker) markListFailed(repo ports.SCMRepo) {
	r.listFailed[prKey(repo, 0)] = true
	r.markFailed(repo)
}

// markOK un-marks a repo as refresh-incomplete. It is used by
// the terminal-reconciliation path: a reconciled PR that turns out to be
// a terminal transition (merged/closed) is persisted normally, and the
// repo ETag/cursor may advance. A "still open" no-op result does NOT
// call this, so the ETag/cursor stay pinned (cross-cutting durable-state
// preservation rule).
//
// Monotonicity guard: if the repo's listing failed this poll
// (listFailed), do NOT flip it back to OK. A successful PR write for
// a different PR must not clear a listing-level failure.
func (r *repoRefreshTracker) markOK(repo ports.SCMRepo) {
	key := prKey(repo, 0)
	if r.listFailed[key] {
		return
	}
	if g, ok := r.guards[key]; ok && g.err == nil && g.result.ETag != "" {
		r.ok[key] = true
	}
}
