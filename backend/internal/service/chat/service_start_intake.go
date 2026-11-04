package chat

import (
	"context"
	"errors"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// startIntake tracks owners whose Start holds the controller gate, so a relayed
// send does not wait out a cold resume (B496). After a daemon restart a resume
// spends many seconds under the gate (provider launch, native history read and
// projection); a `tallpine send` that waited on the gate meanwhile blocked its
// caller for that whole time. Such a send is queued durably instead, exactly as
// one sent while the agent is stopped, and the starting controller delivers it.
type startIntake struct {
	starting map[domain.ConversationOwner]int
	queued   map[domain.ConversationOwner]bool
}

// beginStart marks owner as starting under its gate. The returned func ends
// the mark and reports whether a send was queued behind this start meanwhile.
func (s *Service) beginStart(owner domain.ConversationOwner) func() bool {
	s.mu.Lock()
	if s.startIntake.starting == nil {
		s.startIntake.starting = make(map[domain.ConversationOwner]int)
		s.startIntake.queued = make(map[domain.ConversationOwner]bool)
	}
	s.startIntake.starting[owner]++
	s.mu.Unlock()
	return func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.startIntake.starting[owner]--
		if s.startIntake.starting[owner] <= 0 {
			delete(s.startIntake.starting, owner)
		}
		queued := s.startIntake.queued[owner]
		delete(s.startIntake.queued, owner)
		return queued
	}
}

func (s *Service) startInProgress(owner domain.ConversationOwner) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.startIntake.starting[owner] > 0
}

// startingQueueStore can queue a turn for a controller that is starting now.
// The sqlite store implements it; a store without it keeps the waiting path.
type startingQueueStore interface {
	AppendStartingUserMessage(ctx context.Context, conversationID string, session domain.SessionID, msg domain.ConversationMessage, turnID string, now time.Time) (bool, error)
}

// queueBehindStart queues a relayed message for the controller a concurrent
// Start or wake is bringing up, instead of waiting on the gate or the wake run
// that holds it up. ok is false when nothing is starting, a live controller
// already exists, or the store cannot queue it; the caller then relays normally.
func (s *Service) queueBehindStart(
	ctx context.Context,
	id domain.SessionID,
	text, clientMessageID string,
	options ports.MessageDeliveryOptions,
) (domain.ConversationTurn, bool, error) {
	owner := domain.SessionConversationOwner(id)
	waking := s.isWaking(id)
	if s.HasLiveChatController(id) || (!waking && !s.startHoldsGate(owner)) {
		return domain.ConversationTurn{}, false, nil
	}
	record, err := s.requireChatSession(ctx, id)
	if err != nil || record.IsTerminated {
		return domain.ConversationTurn{}, false, nil //nolint:nilerr // the normal path reports it
	}
	msg := s.relayMessage(ctx, text, clientMessageID, options)
	if msg.ClientPayloadHash == "" {
		if msg.ClientPayloadHash, err = clientPayloadHash(msg); err != nil {
			return domain.ConversationTurn{}, false, nil //nolint:nilerr // the normal path reports it
		}
	}
	turn, err := s.queueForNextController(ctx, record, msg, true)
	if errors.Is(err, ErrNotProvisioning) {
		return domain.ConversationTurn{}, false, nil
	}
	if err != nil {
		return domain.ConversationTurn{}, true, err
	}
	s.mu.Lock()
	if s.startIntake.starting[owner] > 0 {
		s.startIntake.queued[owner] = true
	}
	s.mu.Unlock()
	if waking || record.HibernatedAt != nil {
		// Joins the wake already running and drains once it is up, whichever
		// caller started it; a failed wake keeps the message for its retry.
		s.startBackgroundWake(id)
	}
	s.log.Info("chat send queued behind a starting controller", "session", id, "turn", turn.ID)
	return turn, true, nil
}

// startHoldsGate reports whether a Start for owner holds its gate right now.
func (s *Service) startHoldsGate(owner domain.ConversationOwner) bool {
	if !s.startInProgress(owner) {
		return false
	}
	gate := s.controllerGate(owner)
	if gate.tryLock() {
		gate.unlock()
		return false
	}
	return true
}
