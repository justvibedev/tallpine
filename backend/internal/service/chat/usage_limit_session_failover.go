package chat

import (
	"context"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

const (
	pausedMovePoll    = 2 * time.Second
	pausedMoveTimeout = 5 * time.Minute
)

// failoverPausedSession moves a session whose provider quota is exhausted to an
// account with headroom, when failover is on, so queued automation (reports,
// nudges) does not wait for the reset. The failover service rate-limits per
// session, so repeated calls are cheap. Without a usable account the session
// waits for the reset as before.
func (s *Service) failoverPausedSession(id domain.SessionID, harness domain.AgentHarness) {
	s.mu.RLock()
	failover := s.usageLimitFailover
	s.mu.RUnlock()
	if failover == nil || !failover.Enabled() {
		return
	}
	if _, running := s.pausedFailovers.LoadOrStore(id, struct{}{}); running {
		return
	}
	go func() {
		defer s.pausedFailovers.Delete(id)
		ctx, cancel := context.WithTimeout(context.Background(), pausedMoveTimeout)
		defer cancel()
		if !s.pausedFailoverAllowed(ctx, id) {
			return
		}
		moveID := failover.Failover(ctx, id, harness)
		if moveID == "" {
			return
		}
		ticker := time.NewTicker(pausedMovePoll)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			finished, moved := failover.MoveFinished(ctx, moveID)
			if !finished {
				continue
			}
			if moved {
				s.drainAfterMove(ctx, id)
			}
			return
		}
	}()
}

// drainAfterMove starts the queued turns the pause held back, now that the
// session runs on another account.
func (s *Service) drainAfterMove(ctx context.Context, id domain.SessionID) {
	controller, release, err := s.workingController(ctx, id)
	if err != nil {
		s.log.Warn("drain after usage-limit failover", "session", id, "error", err)
		return
	}
	defer release()
	if err := controller.drainOne(ctx); err != nil {
		s.log.Warn("drain after usage-limit failover", "session", id, "error", err)
	}
}

// pausedFailoverAllowed applies the armed path's session checks, and leaves a
// session whose failed prompt is armed for auto-resume to that path: its move
// retries the prompt on the new account, this one only drains the queue.
func (s *Service) pausedFailoverAllowed(ctx context.Context, id domain.SessionID) bool {
	record, err := s.requireChatSession(ctx, id)
	if err != nil || validateUsageLimitSession(record) != nil {
		return false
	}
	conversation, err := s.store.ConversationForSession(ctx, id)
	if err != nil {
		return false
	}
	if reader, ok := s.store.(usageLimitStore); ok {
		if latest, err := reader.LatestAutoResumeTurn(ctx, conversation.ID); err == nil && latest.UsageLimit != nil && latest.UsageLimit.AutoResume {
			return false
		}
	}
	return true
}

// disarmMovedTurn stops the armed-turn scan from also resuming a turn whose
// failover move finished: the retry is driven from finishFailoverMoves, and a
// plain rebind no longer disarms (B476).
func (s *Service) disarmMovedTurn(ctx context.Context, store usageLimitStore, turnID string) {
	turn, err := s.store.TurnByID(ctx, turnID)
	if err == nil {
		err = store.SetTurnAutoResume(ctx, turn.ConversationID, turnID, nil, false, "")
	}
	if err != nil {
		s.log.Warn("usage-limit failover: could not disarm the moved turn", "turn", turnID, "error", err)
	}
}

func (s *Service) dropFailoverMove(ctx context.Context, turnID string, move failoverMove) {
	s.autoResumeMu.Lock()
	cur, ok := s.failoverMoves[turnID]
	if ok && cur.moveID == move.moveID {
		delete(s.failoverMoves, turnID)
	}
	s.autoResumeMu.Unlock()
	if ok && cur.moveID == move.moveID {
		s.forgetFailoverMove(ctx, turnID)
	}
}
