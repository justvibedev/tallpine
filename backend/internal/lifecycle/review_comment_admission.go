package lifecycle

import (
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

const (
	// turnClaimTTL bounds how long a write that never started a turn holds
	// review comments back once the session is no longer active.
	turnClaimTTL = 15 * time.Minute
	// turnClaimMaxAge is the safety cap on a started claim whose turn end is
	// never observed. A long turn is normal, so this is far longer.
	turnClaimMaxAge = 6 * time.Hour
)

// turnClaim records a message Tallpine wrote to a session that no finished turn has
// consumed yet. While it stands, review comments are deferred instead of sent:
// anything written now would wait in the chat queue behind that turn and be
// dispatched later without a fresh thread check. It is shared by every
// delivery path (observation, turn end, startup restore), so a comment sent
// by one keeps the others from sending another into the same turn.
type turnClaim struct {
	token uint64
	at    time.Time
	// started is set once a turn that will consume the message has begun; the
	// claim is released when that turn ends.
	started   bool
	startedAt time.Time
	// idleSince is when an unstarted claim's session last stopped being
	// active: a write queued behind a running turn is only overdue once that
	// turn has ended and its own turn still has not begun.
	idleSince time.Time
}

// newTurnClaimLocked builds a claim for a write to rec now. A harness that
// steers its running turn consumes the write in that turn, so the claim
// counts as started. Caller holds deferMu.
func (m *Manager) newTurnClaimLocked(rec domain.SessionRecord) turnClaim {
	m.claimSeq++
	now := m.clock()
	c := turnClaim{token: m.claimSeq, at: now}
	if rec.Activity.State == domain.ActivityActive && m.writeSteersRunningTurn(rec) {
		c.started, c.startedAt = true, now
	}
	return c
}

// writeSteersRunningTurn reports whether a write to a session mid-turn is
// consumed by that turn rather than queued behind it. Steering is a terminal
// capability (input typed into the agent's TUI); a Chat session's write goes
// through Controller.Send, which queues it for the next turn whatever the
// harness can do in its TUI.
func (m *Manager) writeSteersRunningTurn(rec domain.SessionRecord) bool {
	return domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat && m.steerActive(rec.Harness)
}

// midTurn reports whether a turn is running: working, or paused on the user
// (an approval or question) in the middle of it.
func midTurn(s domain.ActivityState) bool {
	return s == domain.ActivityActive || s.NeedsInput()
}

// liveTurnClaimLocked returns the session's claim unless it is stale. A claim
// never lapses while the session is mid-turn, however long the turn runs or
// waits on the user: started ones end with their turn, and unstarted ones are
// waiting behind the running turn. A write that never started a turn lapses
// after turnClaimTTL of the session being idle. Caller holds deferMu.
func (m *Manager) liveTurnClaimLocked(rec domain.SessionRecord) (turnClaim, bool) {
	c, ok := m.turnClaims[rec.ID]
	if !ok {
		return turnClaim{}, false
	}
	now := m.clock()
	stale := c.started && now.Sub(c.startedAt) >= turnClaimMaxAge
	if !c.started && !midTurn(rec.Activity.State) {
		since := c.idleSince
		if since.IsZero() {
			since = c.at
		}
		stale = now.Sub(since) >= turnClaimTTL
	}
	if stale {
		delete(m.turnClaims, rec.ID)
		return turnClaim{}, false
	}
	return c, true
}

// claimTurn admits one review-comment write to the session's next turn. It
// fails while another write's turn is still pending. The returned token
// releases the claim if the write does not happen after all.
func (m *Manager) claimTurn(rec domain.SessionRecord) (uint64, bool) {
	m.deferMu.Lock()
	defer m.deferMu.Unlock()
	if _, ok := m.liveTurnClaimLocked(rec); ok {
		return 0, false
	}
	if m.turnClaims == nil {
		m.turnClaims = map[domain.SessionID]turnClaim{}
	}
	c := m.newTurnClaimLocked(rec)
	m.turnClaims[rec.ID] = c
	return c.token, true
}

// admitNudgeWrite records a write that is never deferred (a CI, review,
// merge-conflict, or tracker nudge), so review comments wait for its turn too.
// sendOnce calls it only when the write is about to happen, past dedup and the
// attempt budget: a nudge rebuilt on every poll and deduped must not touch the
// claim, or the claim would never release. A pending claim is kept, but if the
// session is mid-turn and the write does not steer that turn (a Chat
// session, a harness that cannot steer, or a turn paused on the user), this
// write needs a turn after the current one, so the claim must outlive the
// current turn's end. The returned undo restores the previous claim if the guard refuses.
func (m *Manager) admitNudgeWrite(rec domain.SessionRecord) (undo func()) {
	m.deferMu.Lock()
	defer m.deferMu.Unlock()
	if m.turnClaims == nil {
		m.turnClaims = map[domain.SessionID]turnClaim{}
	}
	prev, hadPrev := m.liveTurnClaimLocked(rec)
	next := prev
	switch {
	case !hadPrev:
		next = m.newTurnClaimLocked(rec)
	case midTurn(rec.Activity.State) && (rec.Activity.State != domain.ActivityActive || !m.writeSteersRunningTurn(rec)):
		m.claimSeq++
		next.token, next.at, next.started, next.startedAt, next.idleSince = m.claimSeq, m.clock(), false, time.Time{}, time.Time{}
	}
	m.turnClaims[rec.ID] = next
	return func() {
		m.deferMu.Lock()
		defer m.deferMu.Unlock()
		if cur, ok := m.turnClaims[rec.ID]; !ok || cur.token != next.token {
			return // superseded meanwhile
		}
		if hadPrev {
			m.turnClaims[rec.ID] = prev
		} else {
			delete(m.turnClaims, rec.ID)
		}
	}
}

// releaseTurn drops a claim whose write did not happen. A token of 0, or a
// claim since replaced, is left alone.
func (m *Manager) releaseTurn(id domain.SessionID, token uint64) {
	if token == 0 {
		return
	}
	m.deferMu.Lock()
	defer m.deferMu.Unlock()
	if c, ok := m.turnClaims[id]; ok && c.token == token {
		delete(m.turnClaims, id)
	}
}

// noteTurnActivity advances the session's claim from the activity hook: a turn
// starting marks it started, and that turn ending releases it. A turn starts
// only when the session leaves idle, possibly straight into an approval; a
// running turn resuming after the user answers a prompt is not a new turn. It
// takes only deferMu, so it is safe on the hook path (see
// deliverDeferredAfterTurn).
func (m *Manager) noteTurnActivity(id domain.SessionID, prev, next domain.ActivityState) {
	m.deferMu.Lock()
	defer m.deferMu.Unlock()
	c, ok := m.turnClaims[id]
	if !ok {
		return
	}
	switch {
	case next == domain.ActivityExited:
		delete(m.turnClaims, id)
	case midTurn(next) && !midTurn(prev):
		if !c.started {
			c.started, c.startedAt = true, m.clock()
			m.turnClaims[id] = c
		}
	case next == domain.ActivityIdle && prev != domain.ActivityIdle && c.started:
		delete(m.turnClaims, id)
	case next == domain.ActivityIdle && prev != domain.ActivityIdle:
		c.idleSince = m.clock()
		m.turnClaims[id] = c
	}
}

// forgetTurnClaim drops the session's claim when it ends.
func (m *Manager) forgetTurnClaim(id domain.SessionID) {
	m.deferMu.Lock()
	defer m.deferMu.Unlock()
	delete(m.turnClaims, id)
}
