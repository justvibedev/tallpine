package store

import (
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// Conversation persistence for Chat sessions.
//
// Two invariants live here rather than in the caller:
//
//   - Sequence allocation and the row that uses it commit together. Every write
//     that mints a position runs inside one transaction that bumps
//     conversations.latest_sequence and inserts the row, so two concurrent
//     writers cannot land on the same position.
//   - A projection and the raw provider event that produced it are written in the
//     same transaction. If the process dies between them, neither survives — so
//     the archive can never disagree with the timeline it explains.

// ErrConversationNotFound reports a lookup for a session that has none. It
// aliases the domain sentinel so a service can recognize it without importing the
// storage layer.
var ErrConversationNotFound = domain.ErrNoConversation

// ErrNoQueuedTurn reports an empty send queue. It is an ordinary outcome of
// draining, not a failure.
var ErrNoQueuedTurn = domain.ErrNoQueuedTurn

// ErrQueuedTurnNotAvailable means the selected turn cannot be reserved: it is
// absent from this conversation, no longer queued, or another promotion already
// owns it. The caller must not contact the provider after this outcome.
var ErrQueuedTurnNotAvailable = ports.ErrQueuedTurnNotAvailable
