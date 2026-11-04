package accountadopt

import (
	"context"
	"errors"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	sessionmanager "tallpine.eu/tallpine/backend/internal/session_manager"
)

// start runs a batch in the background unless it already runs.
func (s *Service) start(batchID string) {
	s.mu.Lock()
	if s.running[batchID] {
		s.mu.Unlock()
		return
	}
	s.running[batchID] = true
	ctx := s.runCtx
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		defer func() {
			s.mu.Lock()
			delete(s.running, batchID)
			s.mu.Unlock()
		}()
		s.run(ctx, batchID)
	}()
}

// run reads the batch from the store each round, so it is the same loop after
// a restart. It ends when every session has an outcome.
func (s *Service) run(ctx context.Context, batchID string) {
	for {
		batch, ok, err := s.cfg.Store.GetAccountAdoption(ctx, batchID)
		if err != nil {
			s.cfg.Log.Warn("account adoption: read batch", "batch", batchID, "error", err)
		} else if !ok || batch.State != domain.AccountAdoptionRunning {
			return
		} else if s.advance(ctx, &batch) {
			if err := s.cfg.Store.CompleteAccountAdoption(ctx, batchID); err != nil {
				s.cfg.Log.Warn("account adoption: complete batch", "batch", batchID, "error", err)
			}
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.cfg.Poll):
		}
	}
}

// advance reads the started moves, then starts queued sessions while slots
// remain. It reports whether every session has settled.
func (s *Service) advance(ctx context.Context, batch *domain.AccountAdoption) (settled bool) {
	items := batch.Items
	moving := 0
	for i := range items {
		if st := items[i].State; st == domain.AccountItemMoving || st == domain.AccountItemWaiting {
			s.refresh(ctx, batch, &items[i])
		}
		if items[i].State == domain.AccountItemMoving {
			moving++
		}
	}
	for i := range items {
		if moving >= s.cfg.Slots {
			break
		}
		if items[i].State != domain.AccountItemQueued {
			continue
		}
		// Orchestrators go last: not while a worker still queues or moves. A
		// worker parked on a busy turn does not hold them back.
		if items[i].IsOrchestrator && workerPending(items) {
			break
		}
		s.begin(ctx, batch, &items[i])
		if items[i].State == domain.AccountItemMoving {
			moving++
		}
	}
	for i := range items {
		if !items[i].State.Terminal() {
			return false
		}
	}
	return true
}

func workerPending(items []domain.AccountAdoptionItem) bool {
	for _, it := range items {
		if !it.IsOrchestrator && (it.State == domain.AccountItemQueued || it.State == domain.AccountItemMoving) {
			return true
		}
	}
	return false
}

// refresh reads a started move's durable phase.
func (s *Service) refresh(ctx context.Context, batch *domain.AccountAdoption, item *domain.AccountAdoptionItem) {
	if item.TransitionID == "" {
		// Recorded as moving before the move was asked for, and the daemon died
		// (or the write failed) before its id was saved: the session's active
		// move, if it is ours, is the one to follow; else start over.
		if t, ok := s.activeMoveToAccount(ctx, batch, item.SessionID); ok {
			item.TransitionID = t.ID
			state, code, msg := s.outcome(ctx, batch, item.SessionID, t)
			s.set(ctx, batch, item, state, code, msg)
			return
		}
		s.set(ctx, batch, item, domain.AccountItemQueued, "", "")
		return
	}
	t, ok, err := s.cfg.Mover.GetSessionInterfaceTransition(ctx, item.TransitionID)
	if err != nil {
		s.cfg.Log.Warn("account adoption: read move", "session", item.SessionID, "error", err)
		return
	}
	if !ok {
		s.set(ctx, batch, item, domain.AccountItemFailed, "MOVE_LOST", "the move is no longer recorded")
		return
	}
	state, code, msg := s.outcome(ctx, batch, item.SessionID, t)
	if state != item.State || code != item.ErrorCode {
		s.set(ctx, batch, item, state, code, msg)
	}
}

// activeMoveToAccount is the session's running move when it goes to the batch's account.
func (s *Service) activeMoveToAccount(ctx context.Context, batch *domain.AccountAdoption, id domain.SessionID) (domain.SessionInterfaceTransition, bool) {
	t, ok, err := s.cfg.Mover.GetActiveSessionInterfaceTransition(ctx, id)
	if err != nil || !ok || t.TargetAccountID != batch.AccountID {
		return domain.SessionInterfaceTransition{}, false
	}
	return t, true
}

// begin starts one session's move, or only rebinds a session whose agent is
// not running (it must not be woken up by a switch).
func (s *Service) begin(ctx context.Context, batch *domain.AccountAdoption, item *domain.AccountAdoptionItem) {
	rec, found, err := s.cfg.Sessions.GetSession(ctx, item.SessionID)
	if err != nil {
		s.set(ctx, batch, item, domain.AccountItemFailed, "MOVE_FAILED", err.Error())
		return
	}
	if !found || rec.IsTerminated {
		s.set(ctx, batch, item, domain.AccountItemSkipped, "", "")
		return
	}
	if rec.Activity.State == domain.ActivityExited {
		// A manual move owns the session; do not rebind under it.
		if _, busy, err := s.cfg.Mover.GetActiveSessionInterfaceTransition(ctx, rec.ID); err == nil && busy {
			s.set(ctx, batch, item, domain.AccountItemSkipped, "SESSION_SWITCHING", "another move of this session is in progress")
			return
		}
		if err := s.cfg.Accounts.ValidateAccount(ctx, rec.Harness, batch.AccountID); err != nil {
			state, code, msg := failure(err)
			s.set(ctx, batch, item, state, code, msg)
			return
		}
		if err := s.cfg.Accounts.CommitSession(ctx, rec.ID, rec.Harness, batch.AccountID); err != nil {
			state, code, msg := failure(err)
			s.set(ctx, batch, item, state, code, msg)
			return
		}
		s.set(ctx, batch, item, domain.AccountItemRebound, "", "")
		return
	}
	policy := domain.SessionInterfaceTransitionDrain
	if batch.Policy == domain.AccountAdoptionInterrupt {
		policy = domain.SessionInterfaceTransitionInterrupt
	}
	// Recorded first, so a crash after the move starts cannot lose it.
	s.set(ctx, batch, item, domain.AccountItemMoving, "", "")
	t, err := s.cfg.Mover.StartAccountTransition(ctx, item.SessionID, batch.AccountID, policy)
	if errors.Is(err, sessionmanager.ErrInterfaceTransitionInProgress) {
		// Already moving to this account (a resumed batch): follow that move.
		if active, ok := s.activeMoveToAccount(ctx, batch, item.SessionID); ok {
			t, err = active, nil
		}
	}
	if err != nil {
		state, code, msg := failure(err)
		s.set(ctx, batch, item, state, code, msg)
		return
	}
	item.TransitionID = t.ID
	state, code, msg := s.outcome(ctx, batch, item.SessionID, t)
	s.set(ctx, batch, item, state, code, msg)
}

// outcome is the session's state for a move's phase. A recovery row is final,
// but the move may have committed before it: then the session is moved.
func (s *Service) outcome(ctx context.Context, batch *domain.AccountAdoption, id domain.SessionID, t domain.SessionInterfaceTransition) (domain.AccountAdoptionItemState, string, string) {
	state, code, msg := fromTransition(t)
	if t.Phase == domain.SessionInterfaceTransitionRecovery {
		if account, bound, err := s.cfg.Accounts.SessionAccount(ctx, id, batch.Provider); err == nil && bound && account.ID == batch.AccountID {
			return domain.AccountItemMoved, "", ""
		}
	}
	return state, code, msg
}

func (s *Service) set(ctx context.Context, batch *domain.AccountAdoption, item *domain.AccountAdoptionItem, state domain.AccountAdoptionItemState, code, msg string) {
	item.State, item.ErrorCode, item.ErrorMessage, item.UpdatedAt = state, code, msg, s.cfg.Now()
	if err := s.cfg.Store.UpdateAccountAdoptionItem(ctx, batch.ID, *item); err != nil {
		s.cfg.Log.Warn("account adoption: record outcome", "session", item.SessionID, "error", err)
	}
}
