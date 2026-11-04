package lifecycle

import (
	"sync"

	"tallpine.eu/tallpine/backend/internal/ports"
)

const reviewMaxNudge = 3

type reactionState struct {
	mu       sync.Mutex
	seen     map[string]string
	attempts map[string]int
	// loaded tracks PR URLs whose persisted dedup payload has been merged into
	// seen/attempts during this process. Lazy: we only pay the DB read on the
	// first reaction touching each PR after startup.
	loaded map[string]bool
	// pendingRearm tracks PR URLs whose merge-conflict re-arm has been applied
	// in memory but whose durable write failed. The in-memory delete stands (a
	// conflict returning in this process must still nudge), so without this the
	// next re-arm would see nothing armed, take the early return, and never
	// retry the store — leaving the stale "conflicting" signature on disk to
	// suppress the nudge after a restart. Cleared once the write lands.
	pendingRearm map[string]bool
	// deferred holds review-comment nudges (key -> signature) held back while
	// the worker was mid-turn; they are delivered, or dropped as handled, when
	// the turn ends. Persisted with seen/attempts so a restart keeps them.
	deferred map[string]string
}

func newReactionState() reactionState {
	return reactionState{seen: map[string]string{}, attempts: map[string]int{}, loaded: map[string]bool{}, pendingRearm: map[string]bool{}, deferred: map[string]string{}}
}

// reactionPayload is the JSON document persisted in pr.last_nudge_signature.
// Keeping the schema explicit (and stable) lets the daemon restart and resume
// the existing dedup state without re-nudging an agent.
type reactionPayload struct {
	Seen     map[string]string `json:"seen,omitempty"`
	Attempts map[string]int    `json:"attempts,omitempty"`
	Deferred map[string]string `json:"deferred,omitempty"`
}

// pendingNudge is one actionable PR nudge queued by ApplyPRObservation. Queuing
// each condition's nudge (instead of sending inline and returning) keeps the
// conditions independent — none can suppress another — and centralizes the
// send + dedup in a single loop.
type pendingNudge struct {
	key         string
	sig         string
	msg         string
	maxAttempts int
	// urgent routes the send through sessionguard.NudgeUrgent instead of
	// Nudge: it still reaches a session idle at a needs-input prompt instead
	// of being suppressed until the agent resumes. Reserved for the
	// merge-conflict nudge (see ApplyPRObservation) — every other nudge type
	// keeps the ordinary Nudge gate.
	urgent bool
	// comment marks a review-comment nudge, which goes through
	// deliverReviewComment instead of sendOnce.
	comment *ports.PRCommentObservation
}

const (
	// sendOnceAccounted: the message was sent, or a prior identical send is
	// already recorded (dedup hit) or the attempt budget is spent. In every
	// case the caller may treat the nudge as delivered — nothing more to do.
	sendOnceAccounted sendOnceOutcome = iota
	// sendOnceSuppressed: the just-in-time guard skipped the paste because the
	// session is terminated or awaiting the user (blocked/waiting_input). The
	// message did NOT reach the worker; the caller must not mark it delivered so
	// it re-fires on the next observation once the session is workable again.
	sendOnceSuppressed
	// sendOnceSent: accounted, and written to the worker by this call, which
	// usually starts a turn. Review-comment delivery holds later comments back
	// until that turn ends (see deliverReviewComment).
	sendOnceSent
	// sendOnceBusy: an idle-only write (nudgeIdleOnly) found the session
	// mid-turn at the write boundary. Nothing was written or accounted.
	sendOnceBusy
)

const (
	// nudgeRoutine refuses while the session awaits the user (guard.Nudge).
	nudgeRoutine nudgePolicy = iota
	// nudgeUrgent still reaches a session idle at a needs-input prompt
	// (guard.NudgeUrgent).
	nudgeUrgent
	// nudgeIdleOnly also refuses a session mid-turn, re-read just before the
	// write: a review comment must not be queued behind a turn that may
	// resolve its thread, even if the turn began after the caller's check.
	nudgeIdleOnly
)
