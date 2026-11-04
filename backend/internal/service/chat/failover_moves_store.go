package chat

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// failoverMoveStore keeps the retries owed after a failover move across a
// daemon restart. A store without it keeps them in memory only.
type failoverMoveStore interface {
	SaveAccountFailoverRetry(context.Context, domain.AccountFailoverRetry) error
	DeleteAccountFailoverRetry(context.Context, string) error
	ListAccountFailoverRetries(context.Context) ([]domain.AccountFailoverRetry, error)
}

// persistFailoverMove records the move so a restart does not lose its retry.
// A failed write is logged and the in-memory entry still works until restart.
func (s *Service) persistFailoverMove(ctx context.Context, turnID string, move failoverMove) {
	store, ok := s.store.(failoverMoveStore)
	if !ok {
		return
	}
	err := store.SaveAccountFailoverRetry(ctx, domain.AccountFailoverRetry{
		TurnID: turnID, SessionID: move.session, MoveID: move.moveID, Moved: move.moved,
		Attempts: move.attempts, RetryAt: move.retryAt, CreatedAt: s.now(),
	})
	if err != nil {
		s.log.Warn("usage-limit failover: could not record the move; its retry is lost if the daemon restarts", "turn", turnID, "error", err)
	}
}

// forgetFailoverMove removes the record once the retry was delivered or can no
// longer happen.
func (s *Service) forgetFailoverMove(ctx context.Context, turnID string) {
	store, ok := s.store.(failoverMoveStore)
	if !ok {
		return
	}
	if err := store.DeleteAccountFailoverRetry(ctx, turnID); err != nil {
		s.log.Warn("usage-limit failover: could not clear a finished move record", "turn", turnID, "error", err)
	}
}

// loadFailoverMoves brings the recorded moves back after a restart, once. A
// failed read is tried again on the next scan.
func (s *Service) loadFailoverMoves(ctx context.Context) {
	store, ok := s.store.(failoverMoveStore)
	if !ok {
		return
	}
	s.autoResumeMu.Lock()
	loaded := s.failoverMovesLoaded
	s.autoResumeMu.Unlock()
	if loaded {
		return
	}
	records, err := store.ListAccountFailoverRetries(ctx)
	if err != nil {
		s.log.Warn("usage-limit failover: could not read recorded moves", "error", err)
		return
	}
	s.autoResumeMu.Lock()
	defer s.autoResumeMu.Unlock()
	if s.failoverMovesLoaded {
		return
	}
	s.failoverMovesLoaded = true
	if s.failoverMoves == nil {
		s.failoverMoves = make(map[string]failoverMove)
	}
	for _, r := range records {
		if _, live := s.failoverMoves[r.TurnID]; live {
			continue
		}
		s.failoverMoves[r.TurnID] = failoverMove{
			session: r.SessionID, moveID: r.MoveID, moved: r.Moved, attempts: r.Attempts, retryAt: r.RetryAt,
		}
	}
}
