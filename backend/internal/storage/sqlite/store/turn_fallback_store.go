package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

func insertTurnFallback(ctx context.Context, q *gen.Queries, f domain.TurnFallback) error {
	return q.InsertTurnFallback(ctx, gen.InsertTurnFallbackParams{ID: f.StableID(), SessionID: string(f.SessionID), Mode: string(f.Mode), Generation: f.Generation, NativeID: f.NativeID, TurnID: f.TurnID})
}

// RecordChatTurnFallback participates in the provider archive/projection
// transaction. Only the controller's accepted primary completion calls it.
func (s *Store) RecordChatTurnFallback(ctx context.Context, conversationID, providerTurnID, excerpt string, at time.Time) error {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	turn, err := q.SelectConversationTurnByProviderID(ctx, gen.SelectConversationTurnByProviderIDParams{ConversationID: conversationID, ProviderTurnID: providerTurnID})
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	rec, err := q.GetSession(ctx, turn.HandledBySessionID)
	if err != nil {
		return err
	}
	if rec.Kind != domain.KindWorker || rec.IsTerminated || turn.HandledByReviewID.Valid {
		return nil
	}
	if excerpt == "" {
		text, readErr := q.LatestTurnFallbackAssistant(ctx, sql.NullString{String: turn.ID, Valid: true})
		if readErr != nil && !errors.Is(readErr, sql.ErrNoRows) {
			return readErr
		}
		excerpt = text
	}
	f := domain.TurnFallback{SessionID: turn.HandledBySessionID, Mode: domain.SessionModeChat, Generation: rec.ControllerGeneration, TurnID: turn.ID}
	if err := establishChatFallbackOwner(ctx, q, f); err != nil {
		return err
	}
	return q.CompleteTurnFallback(ctx, gen.CompleteTurnFallbackParams{ID: f.StableID(), CompletedAt: nullTime(at), DueAt: nullTime(at.Add(domain.ChatTurnFallbackDelay())), Excerpt: domain.TurnFallbackExcerpt(excerpt)})
}

// SupersedeCompletedTurnFallbacks retires the session's completed results when
// work starts without a user message (a harness-started turn), as intake does
// for a prompted one. Otherwise the last prompted turn's row still reads as the
// durable owner of the next turn_ended and the new turn is never reported.
func (s *Store) SupersedeCompletedTurnFallbacks(ctx context.Context, id domain.SessionID) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "supersede completed turn notifications", func(q *gen.Queries) error {
		return q.SupersedeCompletedFallbacks(ctx, string(id))
	})
}

// HasCurrentCompletedTurnFallback determines whether a durable completion owns
// the current turn's notification, including reports already emitted/suppressed.
// Apply the same new-work and ownership fences as delivery before answering.
func (s *Store) HasCurrentCompletedTurnFallback(ctx context.Context, id domain.SessionID) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var found bool
	err := s.inTx(ctx, "check completed turn notification", func(q *gen.Queries) error {
		if err := supersedeInvalidTurnFallbacks(ctx, q); err != nil {
			return err
		}
		var err error
		found, err = q.HasCurrentCompletedTurnFallback(ctx, string(id))
		return err
	})
	return found, err
}

// ListPendingTurnFallbacks also retires intents fenced by committed newer work.
func (s *Store) ListPendingTurnFallbacks(ctx context.Context) ([]domain.TurnFallback, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var rows []gen.TurnFallback
	err := s.inTx(ctx, "recover turn fallbacks", func(q *gen.Queries) error {
		if err := supersedeInvalidTurnFallbacks(ctx, q); err != nil {
			return err
		}
		var err error
		rows, err = q.ListPendingTurnFallbacks(ctx)
		return err
	})
	if err != nil {
		return nil, err
	}
	result := make([]domain.TurnFallback, 0, len(rows))
	for _, r := range rows {
		result = append(result, domain.TurnFallback{ID: r.ID, SessionID: domain.SessionID(r.SessionID), Mode: domain.SessionMode(r.Mode), Generation: r.Generation, NativeID: r.NativeID, TurnID: r.TurnID, CompletedAt: r.CompletedAt.Time, DueAt: r.DueAt.Time, Excerpt: r.Excerpt})
	}
	return result, nil
}

// suppressReportFallback runs in the explicit report's transaction. Suppression
// survives a crash before the observer callback, including a report before Stop.
func suppressReportFallback(ctx context.Context, q *gen.Queries, r domain.ReportRecord) error {
	if r.Synthesized || (r.State != domain.ReportDone && r.State != domain.ReportNeedsInput && r.State != domain.ReportStuck) {
		return nil
	}
	session, err := q.GetSession(ctx, r.SessionID)
	if err != nil {
		return err
	}
	if err := supersedeInvalidTurnFallbacks(ctx, q); err != nil {
		return err
	}
	if err := q.SuppressCarriedTurnFallbacks(ctx, string(r.SessionID)); err != nil {
		return err
	}
	if domain.NormalizeSessionMode(session.SessionMode) == domain.SessionModeTUI {
		// An upgrade can leave an accepted native prompt checkpoint without its
		// ledger row. Reconstruct only that exact turn, never the next unknown Stop.
		if session.Harness == domain.HarnessCodex && session.RuntimeLaunchID != "" && session.AgentSessionID != "" &&
			session.ConversationCheckpointGeneration == session.RuntimeLaunchID &&
			session.ConversationCheckpointNativeID == session.AgentSessionID && session.ConversationCheckpointTurnID != "" &&
			!session.ConversationCheckpointUnsettled && session.ConversationCheckpointState.Trusted() {
			f := domain.TurnFallback{SessionID: r.SessionID, Mode: domain.SessionModeTUI, Generation: session.RuntimeLaunchID,
				NativeID: session.AgentSessionID, TurnID: session.ConversationCheckpointTurnID}
			if err := insertTurnFallback(ctx, q, f); err != nil {
				return err
			}
		}
		return q.SuppressTerminalFallback(ctx, gen.SuppressTerminalFallbackParams{SessionID: string(r.SessionID), Generation: session.RuntimeLaunchID})
	}
	turn, err := q.CurrentReportableChatTurn(ctx, string(r.SessionID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	// A retired controller's old final report cannot suppress new work.
	f := domain.TurnFallback{SessionID: r.SessionID, Mode: domain.SessionModeChat, Generation: session.ControllerGeneration, TurnID: turn.ID}
	if turn.ControllerGeneration != session.ControllerGeneration {
		carried, err := q.GetTurnFallback(ctx, f.StableID())
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if carried.Generation != session.ControllerGeneration {
			return nil
		}
	}
	if err := insertTurnFallback(ctx, q, f); err != nil {
		return err
	}
	return q.SuppressTurnFallback(ctx, f.StableID())
}

func recordTerminalFallback(ctx context.Context, q *gen.Queries, f domain.TurnFallback) error {
	_, err := q.GetTurnFallback(ctx, f.StableID())
	fresh := errors.Is(err, sql.ErrNoRows)
	if err != nil && !fresh {
		return err
	}
	if fresh {
		if err := q.SupersedeTerminalFallbacks(ctx, gen.SupersedeTerminalFallbacksParams{SessionID: string(f.SessionID), ID: f.StableID()}); err != nil {
			return err
		}
		if err := insertTurnFallback(ctx, q, f); err != nil {
			return err
		}
	}
	if f.CompletedAt.IsZero() {
		return nil
	}
	return q.CompleteTurnFallback(ctx, gen.CompleteTurnFallbackParams{ID: f.StableID(), CompletedAt: nullTime(f.CompletedAt), DueAt: nullTime(f.CompletedAt.Add(domain.TurnFallbackDelay)), Excerpt: domain.TurnFallbackExcerpt(f.Excerpt)})
}

// Both origin-owned and explicitly carried intents are checked in the same
// transaction as delivery, exit deduplication, or an ownership transfer.
func supersedeInvalidTurnFallbacks(ctx context.Context, q *gen.Queries) error {
	if err := q.SupersedeInvalidTurnFallbacks(ctx); err != nil {
		return err
	}
	return q.SupersedeInvalidCarriedTurnFallbacks(ctx)
}

// TurnFallbackHandoffGeneration returns only the generation reserved by the
// committed interface epoch. It never creates a binding for an arbitrary claim.
// An empty transition ID is ordinary startup recovery: only an unclaimed,
// explicitly rolled-back recovery epoch can supply its first generation.
func (s *Store) TurnFallbackHandoffGeneration(ctx context.Context, id domain.SessionID, expected domain.SessionControllerOwner, transitionID string) (string, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var generation string
	err := s.inTx(ctx, "read fallback handoff claim", func(q *gen.Queries) error {
		session, err := q.GetSession(ctx, id)
		if err != nil {
			return err
		}
		if rowToRecord(session).ControllerOwner() != expected {
			return errors.New("fallback handoff owner changed")
		}
		if err := supersedeInvalidTurnFallbacks(ctx, q); err != nil {
			return err
		}
		f, err := q.GetTurnFallbackHandoffClaim(ctx, string(id))
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		transition, err := q.GetLatestSessionInterfaceTransition(ctx, id)
		if err != nil {
			return err
		}
		if transition.ID != f.CarryTransitionID {
			return nil
		}
		current := session.RuntimeLaunchID
		if session.SessionMode == domain.SessionModeChat {
			current = session.ControllerGeneration
		}
		if transitionID == "" {
			if transition.Phase != domain.SessionInterfaceTransitionRecovery || f.OwnerClaimed != 0 || current != "" || session.SessionMode != transition.SourceMode {
				return nil
			}
		} else if transitionID != transition.ID || transition.Phase.Terminal() {
			return nil
		}
		if current != "" && current != f.OwnerGeneration {
			return nil
		}
		generation = f.OwnerGeneration
		return nil
	})
	return generation, err
}

// BindPrimaryChatTurnToProvider commits ordinary dispatch acceptance and report
// ownership together. Streamed starts instead join their archive transaction.
func (s *Store) BindPrimaryChatTurnToProvider(ctx context.Context, turnID, providerTurnID, generation string, now time.Time) error {
	bind := func(txCtx context.Context, q *gen.Queries) error {
		turn, err := q.SelectConversationTurnByID(txCtx, turnID)
		if err != nil {
			return err
		}
		owner, err := q.GetSession(txCtx, turn.HandledBySessionID)
		if err != nil {
			return err
		}
		if owner.ControllerGeneration != generation || generation == "" || owner.SessionMode != domain.SessionModeChat || owner.IsTerminated {
			return errors.New("primary Chat turn owner changed before bind")
		}
		if err := s.BindTurnToProvider(txCtx, turnID, providerTurnID, now); err != nil {
			return err
		}
		turn, err = q.SelectConversationTurnByID(txCtx, turnID)
		if err != nil {
			return err
		}
		return recordActiveChatTurnFallback(txCtx, q, turn)
	}

	if q, ok := ctx.Value(conversationProjectionTxKey{}).(*gen.Queries); ok && q != nil {
		return bind(ctx, q)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "bind primary Chat turn ownership", func(q *gen.Queries) error {
		return bind(context.WithValue(ctx, conversationProjectionTxKey{}, q), q)
	})
}

// RecordActiveChatTurnFallback establishes suppression ownership for an accepted
// live primary start, after its bind/adopt within the archive transaction.
func (s *Store) RecordActiveChatTurnFallback(ctx context.Context, conversationID, providerTurnID string) error {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	turn, err := q.SelectConversationTurnByProviderID(ctx, gen.SelectConversationTurnByProviderIDParams{ConversationID: conversationID, ProviderTurnID: providerTurnID})
	if err != nil {
		return err
	}
	return recordActiveChatTurnFallback(ctx, q, turn)
}

func recordActiveChatTurnFallback(ctx context.Context, q *gen.Queries, turn gen.ConversationTurn) error {
	session, err := q.GetSession(ctx, turn.HandledBySessionID)
	if err != nil {
		return err
	}
	// The caller proved current ownership through archive fencing or explicit
	// dispatch generation. Queued intake can retain its earlier turn generation.
	// Replayed terminal starts must not resurrect an already handled result.
	if session.Kind != domain.KindWorker || session.IsTerminated || session.SessionMode != domain.SessionModeChat ||
		turn.HandledByReviewID.Valid || turn.State != domain.TurnStateRunning || turn.ProviderTurnID == "" ||
		session.ControllerGeneration == "" || turn.RolledBackAt.Valid {
		return nil
	}
	return establishChatFallbackOwner(ctx, q, domain.TurnFallback{SessionID: turn.HandledBySessionID, Mode: domain.SessionModeChat, Generation: session.ControllerGeneration, TurnID: turn.ID})
}

// establishChatFallbackOwner advances only on first acceptance of an exact
// primary identity. A duplicate start, completion or live reclaim must preserve
// suppression and retirement, including retirement by subsequent queued intake.
// The caller holds the intake/bind/archive/generation transaction.
func establishChatFallbackOwner(ctx context.Context, q *gen.Queries, f domain.TurnFallback) error {
	_, err := q.GetTurnFallback(ctx, f.StableID())
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err := q.SupersedeTerminalFallbacks(ctx, gen.SupersedeTerminalFallbacksParams{SessionID: string(f.SessionID), ID: f.StableID()}); err != nil {
		return err
	}
	return insertTurnFallback(ctx, q, f)
}
