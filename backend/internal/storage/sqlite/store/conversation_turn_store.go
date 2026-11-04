package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// MarkTurnDispatching removes a turn from automatic queue replay before any
// provider call. If the later provider-ID binding fails, delivery is uncertain
// but the prompt cannot be sent a second time by a reconnected controller.
func (s *Store) MarkTurnDispatching(ctx context.Context, turnID string) error {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	if err := q.MarkConversationTurnStarted(ctx, gen.MarkConversationTurnStartedParams{
		ID: turnID,
	}); err != nil {
		return fmt.Errorf("mark turn %s dispatching: %w", turnID, err)
	}
	return nil
}

// BindTurnToProvider records the provider's turn id once a send is accepted and
// marks the turn running.
func (s *Store) BindTurnToProvider(ctx context.Context, turnID, providerTurnID string, now time.Time) error {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	if err := q.BindConversationTurnProviderID(ctx, gen.BindConversationTurnProviderIDParams{
		ProviderTurnID: providerTurnID,
		StartedAt:      sql.NullTime{Time: now, Valid: true},
		ID:             turnID,
	}); err != nil {
		return fmt.Errorf("bind turn %s to provider turn %s: %w", turnID, providerTurnID, err)
	}
	if err := q.MarkConversationTurnStarted(ctx, gen.MarkConversationTurnStartedParams{
		StartedAt: sql.NullTime{Time: now, Valid: true},
		ID:        turnID,
	}); err != nil {
		return fmt.Errorf("mark turn %s started: %w", turnID, err)
	}
	return nil
}

// SettleTurn records a turn's terminal state. An interrupted turn is not an
// error and carries no message.
func (s *Store) SettleTurn(
	ctx context.Context,
	conversationID, providerTurnID string,
	state domain.TurnState,
	errMessage string,
	now time.Time,
) error {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()

	turn, err := q.SelectConversationTurnByProviderID(ctx,
		gen.SelectConversationTurnByProviderIDParams{
			ConversationID: conversationID,
			ProviderTurnID: providerTurnID,
		})
	if errors.Is(err, sql.ErrNoRows) {
		// A turn Tallpine never recorded, e.g. one started by a previous controller
		// before a restart. Not an error: there is nothing to settle.
		return nil
	}
	if err != nil {
		return fmt.Errorf("select turn %s: %w", providerTurnID, err)
	}

	// A history replay can re-settle a failed turn without its cause; that must
	// not erase the provider's error (a usage limit's reset time lives there).
	if errMessage == "" && state == domain.TurnStateFailed && turn.State == domain.TurnStateFailed {
		errMessage = turn.ErrorMessage
	}
	if err := q.SettleConversationTurn(ctx, gen.SettleConversationTurnParams{
		State:        state,
		ErrorMessage: errMessage,
		CompletedAt:  sql.NullTime{Time: now, Valid: true},
		ID:           turn.ID,
	}); err != nil {
		return fmt.Errorf("settle turn %s: %w", turn.ID, err)
	}
	if err := q.SettleStreamingConversationMessagesForTurn(ctx,
		gen.SettleStreamingConversationMessagesForTurnParams{
			UpdatedAt:      now,
			ConversationID: conversationID,
			TurnID:         sql.NullString{String: turn.ID, Valid: true},
		}); err != nil {
		return fmt.Errorf("settle streaming messages for turn %s: %w", turn.ID, err)
	}
	if err := q.SettleRunningConversationActivitiesForTurn(ctx,
		gen.SettleRunningConversationActivitiesForTurnParams{
			Status:         terminalActivityStatus(state),
			UpdatedAt:      now,
			ConversationID: conversationID,
			TurnID:         sql.NullString{String: turn.ID, Valid: true},
		}); err != nil {
		return fmt.Errorf("settle running activities for turn %s: %w", turn.ID, err)
	}
	if turn.HandledByReviewID.Valid {
		if err := q.FailUnsubmittedReviewBatchForChatTurn(ctx, turn.ID); err != nil {
			return fmt.Errorf("settle unsubmitted review batch for turn %s: %w", turn.ID, err)
		}
	}
	if state == domain.TurnStateCompleted {
		if err := finalizeCompletedTurnPlan(ctx, q, turn, now); err != nil {
			return err
		}
	}
	return nil
}

// finalizeCompletedTurnPlan reconciles the two durable plan projections when a
// provider completes a turn without sending a final plan notification. The raw
// provider event remains unchanged; this only records Tallpine's terminal inference.
func finalizeCompletedTurnPlan(
	ctx context.Context,
	q *gen.Queries,
	turn gen.ConversationTurn,
	now time.Time,
) error {
	if turn.PlanJson == "" {
		return nil
	}
	var plan domain.ConversationPlan
	if err := json.Unmarshal([]byte(turn.PlanJson), &plan); err != nil {
		return fmt.Errorf("decode plan for completed turn %s: %w", turn.ID, err)
	}
	if plan.SpansTurns {
		// The agent keeps this list across turns, so its open steps are still open
		// work: the turn ending is not the list ending.
		return nil
	}
	changed := false
	for i := range plan.Steps {
		if plan.Steps[i].Status != domain.PlanStepCompleted {
			plan.Steps[i].Status = domain.PlanStepCompleted
			changed = true
		}
	}
	if !changed {
		return nil
	}

	encodedPlan, err := json.Marshal(plan)
	if err != nil {
		return fmt.Errorf("encode finalized plan for turn %s: %w", turn.ID, err)
	}
	if _, err := q.UpdateConversationTurnPlan(ctx, gen.UpdateConversationTurnPlanParams{
		PlanJson:       string(encodedPlan),
		ConversationID: turn.ConversationID,
		ProviderTurnID: turn.ProviderTurnID,
	}); err != nil {
		return fmt.Errorf("finalize turn plan %s: %w", turn.ID, err)
	}

	detail := map[string]any{"event": "plan", "steps": plan.Steps}
	if plan.Explanation != "" {
		detail["explanation"] = plan.Explanation
	}
	encodedDetail, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("encode finalized plan activity for turn %s: %w", turn.ID, err)
	}
	if err := q.FinalizeConversationPlanActivity(ctx, gen.FinalizeConversationPlanActivityParams{
		Summary:        fmt.Sprintf("Plan %d/%d steps done", len(plan.Steps), len(plan.Steps)),
		DetailJson:     string(encodedDetail),
		UpdatedAt:      now,
		ConversationID: turn.ConversationID,
		TurnID:         sql.NullString{String: turn.ID, Valid: true},
	}); err != nil {
		return fmt.Errorf("finalize plan activity for turn %s: %w", turn.ID, err)
	}
	return nil
}

// terminalActivityStatus mirrors why the enclosing turn ended. Interrupted work
// is neither successful nor failed: it stopped because the user asked it to.
func terminalActivityStatus(state domain.TurnState) domain.ActivityStatus {
	switch state {
	case domain.TurnStateCompleted:
		return domain.ActivityStatusCompleted
	case domain.TurnStateRecovered:
		return domain.ActivityStatusRecovered
	case domain.TurnStateInterrupted:
		return domain.ActivityStatusCancelled
	default:
		return domain.ActivityStatusFailed
	}
}

// SettleOrphanedTurns marks anything a dead controller left in flight. A turn the
// controller was running is not evidence the work finished, so it settles as
// failed rather than silently completing. Queued turns fail too: callers use it
// when the session's queue must not reach the provider (an interface handoff).
func (s *Store) SettleOrphanedTurns(ctx context.Context, session domain.SessionID, now time.Time) error {
	return s.settleOrphanedTurns(ctx, session, now, true)
}

// SettleOrphanedRunningTurns is SettleOrphanedTurns for a controller restart:
// the running turn died with its provider, but queued turns never reached it
// and stay queued for the replacement controller to deliver.
func (s *Store) SettleOrphanedRunningTurns(ctx context.Context, session domain.SessionID, now time.Time) error {
	return s.settleOrphanedTurns(ctx, session, now, false)
}

func (s *Store) settleOrphanedTurns(ctx context.Context, session domain.SessionID, now time.Time, includeQueued bool) error {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	if err := q.FailOrphanedConversationActivities(ctx,
		gen.FailOrphanedConversationActivitiesParams{
			UpdatedAt:          now,
			HandledBySessionID: session,
		}); err != nil {
		return fmt.Errorf("settle orphaned activities for %s: %w", session, err)
	}
	completedAt := sql.NullTime{Time: now, Valid: true}
	var err error
	if includeQueued {
		err = q.SettleOrphanedConversationTurns(ctx, gen.SettleOrphanedConversationTurnsParams{
			CompletedAt: completedAt, HandledBySessionID: session,
		})
	} else {
		err = q.SettleOrphanedRunningConversationTurns(ctx, gen.SettleOrphanedRunningConversationTurnsParams{
			CompletedAt: completedAt, HandledBySessionID: session,
		})
	}
	if err != nil {
		return fmt.Errorf("settle orphaned turns for %s: %w", session, err)
	}
	return nil
}

// SettleUnboundRunningTurn closes an ambiguous dispatch after live reconnect.
// Recheck under the writer lock so a bound or queued turn is never settled.
func (s *Store) SettleUnboundRunningTurn(ctx context.Context, conversationID string, session domain.SessionID, turnID string, now time.Time) error {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	turn, err := q.SelectConversationTurnByID(ctx, turnID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("select unbound running turn %s: %w", turnID, err)
	}
	if turn.ConversationID != conversationID || turn.HandledBySessionID != session || turn.State != domain.TurnStateRunning || turn.ProviderTurnID != "" || turn.RolledBackAt.Valid {
		return nil
	}
	if err := q.SettleConversationTurn(ctx, gen.SettleConversationTurnParams{
		State: domain.TurnStateFailed, ErrorMessage: "provider delivery unconfirmed after reconnect",
		CompletedAt: sql.NullTime{Time: now, Valid: true}, ID: turnID,
	}); err != nil {
		return fmt.Errorf("settle unbound running turn %s: %w", turnID, err)
	}
	return nil
}

// CleanupOwnedControllerWork settles work only when the closing controller still
// owns its session generation. Branch/interface handoffs start the replacement
// before closing the source, so an unconditional stream-end cleanup would
// otherwise fail the replacement's turns and requests. Project conversations can
// be rebound to another session before the old stream ends; request cleanup is
// therefore scoped through turns handled by this session, not current ownership
// of the conversation.
//
// Startup deliberately uses SettleOrphanedTurns and FailPending* directly: no
// controller owns the abandoned generation then, and that repair must remain
// unconditional.
func (s *Store) CleanupOwnedControllerWork(
	ctx context.Context,
	session domain.SessionID,
	conversationID, generation string,
	now time.Time,
) (owned bool, err error) {
	if q, ok := ctx.Value(conversationProjectionTxKey{}).(*gen.Queries); ok && q != nil {
		return cleanupOwnedControllerWork(ctx, q, session, conversationID, generation, now)
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	err = s.inTx(ctx, "clean up owned Chat controller work", func(q *gen.Queries) error {
		var cleanupErr error
		owned, cleanupErr = cleanupOwnedControllerWork(
			ctx, q, session, conversationID, generation, now)
		return cleanupErr
	})
	return owned, err
}

func cleanupOwnedControllerWork(
	ctx context.Context,
	q *gen.Queries,
	session domain.SessionID,
	conversationID, generation string,
	now time.Time,
) (bool, error) {
	owner, err := q.GetSession(ctx, session)
	if err != nil {
		return false, fmt.Errorf("read controller generation for %s: %w", session, err)
	}
	if owner.ControllerGeneration != generation {
		return false, nil
	}
	if err := q.FailOrphanedConversationActivities(ctx,
		gen.FailOrphanedConversationActivitiesParams{
			UpdatedAt: now, HandledBySessionID: session,
		}); err != nil {
		return false, fmt.Errorf("settle orphaned activities for %s: %w", session, err)
	}
	if err := q.SettleOrphanedConversationTurns(ctx,
		gen.SettleOrphanedConversationTurnsParams{
			CompletedAt:        sql.NullTime{Time: now, Valid: true},
			HandledBySessionID: session,
		}); err != nil {
		return false, fmt.Errorf("settle orphaned turns for %s: %w", session, err)
	}
	if err := q.FailPendingConversationRequestsForSession(ctx,
		gen.FailPendingConversationRequestsForSessionParams{
			UpdatedAt:            now,
			TargetConversationID: conversationID,
			HandledBySessionID:   session,
		}); err != nil {
		return false, fmt.Errorf("fail pending requests for %s on %s: %w", session, conversationID, err)
	}
	return true, nil
}

// CleanupOwnedReviewControllerWork uses the review's generation fence. Reviewer
// Chat never claims the worker session's controller generation, so the session
// cleanup above cannot settle its in-flight turn when the reviewer stops.
func (s *Store) CleanupOwnedReviewControllerWork(
	ctx context.Context,
	reviewID, conversationID, generation string,
	now time.Time,
) (owned bool, err error) {
	if q, ok := ctx.Value(conversationProjectionTxKey{}).(*gen.Queries); ok && q != nil {
		return cleanupOwnedReviewControllerWork(ctx, q, reviewID, conversationID, generation, now)
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	err = s.inTx(ctx, "clean up owned reviewer Chat work", func(q *gen.Queries) error {
		var cleanupErr error
		owned, cleanupErr = cleanupOwnedReviewControllerWork(ctx, q, reviewID, conversationID, generation, now)
		return cleanupErr
	})
	return owned, err
}

func cleanupOwnedReviewControllerWork(
	ctx context.Context,
	q *gen.Queries,
	reviewID, conversationID, generation string,
	now time.Time,
) (bool, error) {
	owner, err := q.GetReviewByID(ctx, reviewID)
	if err != nil {
		return false, fmt.Errorf("read reviewer controller generation for %s: %w", reviewID, err)
	}
	// The generation is the ownership fence. A Chat controller may be stopped
	// after the review row has already switched to Terminal, so checking the
	// current interface mode here would skip cleanup for the very transition
	// this method is meant to settle.
	if owner.ControllerGeneration != generation {
		return false, nil
	}
	review := sql.NullString{String: reviewID, Valid: true}
	if err := q.FailOrphanedReviewActivities(ctx, gen.FailOrphanedReviewActivitiesParams{
		UpdatedAt: now, ConversationID: conversationID, ReviewID: review,
	}); err != nil {
		return false, fmt.Errorf("settle reviewer activities for %s: %w", reviewID, err)
	}
	if err := q.SettleOrphanedReviewTurns(ctx, gen.SettleOrphanedReviewTurnsParams{
		CompletedAt: sql.NullTime{Time: now, Valid: true}, ConversationID: conversationID, ReviewID: review,
	}); err != nil {
		return false, fmt.Errorf("settle reviewer turns for %s: %w", reviewID, err)
	}
	if err := q.FailPendingReviewRequests(ctx, gen.FailPendingReviewRequestsParams{
		UpdatedAt: now, ConversationID: conversationID, ReviewID: review,
	}); err != nil {
		return false, fmt.Errorf("settle reviewer requests for %s: %w", reviewID, err)
	}
	return true, nil
}

// ListVisibleRunningTurnProviderIDs returns the same active-branch running turns,
// in the same order, that a conversation snapshot exposes to clients. Interrupt
// uses the full set because a root and nested provider turn may overlap; settling
// only one would leave the UI's Working state behind.
func (s *Store) ListVisibleRunningTurnProviderIDs(
	ctx context.Context,
	conversationID string,
) ([]string, error) {
	providerTurnIDs, err := s.qr.ListVisibleRunningTurnsForConversation(ctx, conversationID)
	if err != nil {
		return nil, fmt.Errorf("list visible running turns for %s: %w", conversationID, err)
	}
	return providerTurnIDs, nil
}

// SetConversationSettings records the provider choices for the next turn.
//
// An empty field is stored as NULL rather than as an empty string, so "the user
// cleared this" and "the user never chose" stay the same thing: fall back to the
// provider's default.
func (s *Store) SetConversationSettings(
	ctx context.Context,
	conversationID string,
	settings domain.ConversationSettings,
	now time.Time,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.UpdateConversationTurnSettings(ctx, gen.UpdateConversationTurnSettingsParams{
		Model:           nullableString(settings.Model),
		ReasoningEffort: nullableString(settings.ReasoningEffort),
		ApprovalMode:    nullableString(string(settings.ApprovalMode)),
		OpencodeMode:    settings.OpenCodeMode,
		UpdatedAt:       now,
		ID:              conversationID,

		AutoCompactWindow: settings.AutoCompactWindow,
	}); err != nil {
		return fmt.Errorf("set conversation settings for %s: %w", conversationID, err)
	}
	return nil
}

// MarkCompacted records that the conversation's history was summarized to reclaim
// context.
//
// Separate from the timeline row the caller writes alongside it, and deliberately
// not folded into UpsertActivity: this is conversation state ("has compaction ever
// run"), the activity is the narrative ("here is where it happened"), and a
// generic activity writer has no business knowing which kinds move a column on the
// conversation.
func (s *Store) MarkCompacted(ctx context.Context, conversationID string, at time.Time) error {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	if err := q.MarkConversationCompacted(ctx, gen.MarkConversationCompactedParams{
		CompactedAt: sql.NullTime{Time: at, Valid: true},
		UpdatedAt:   at,
		ID:          conversationID,
	}); err != nil {
		return fmt.Errorf("mark conversation %s compacted: %w", conversationID, err)
	}
	return nil
}

// SettleTurnByID records a terminal state for a turn Tallpine can name directly.
//
// Needed for a turn that never reached the provider: it has no provider turn id,
// so it cannot be found the way a running turn is. Settling those by the empty
// provider id would match an arbitrary undispatched turn instead of this one.
func (s *Store) SettleTurnByID(
	ctx context.Context,
	turnID string,
	state domain.TurnState,
	errMessage string,
	now time.Time,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "settle turn by id", func(q *gen.Queries) error {
		if err := q.SettleConversationTurn(ctx, gen.SettleConversationTurnParams{
			State:        state,
			ErrorMessage: errMessage,
			CompletedAt:  sql.NullTime{Time: now, Valid: true},
			ID:           turnID,
		}); err != nil {
			return fmt.Errorf("settle turn %s: %w", turnID, err)
		}
		// A reviewer turn the provider refused to start (a queued dispatch that
		// failed after its sender returned) must fail its pass with the error,
		// as SettleTurn does for a turn that ran.
		turn, err := q.SelectConversationTurnByID(ctx, turnID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("select turn %s: %w", turnID, err)
		}
		if turn.HandledByReviewID.Valid {
			if err := q.FailUnsubmittedReviewBatchForChatTurn(ctx, turn.ID); err != nil {
				return fmt.Errorf("settle unsubmitted review batch for turn %s: %w", turn.ID, err)
			}
		}
		return nil
	})
}

// SettleReviewChatWork closes the previous review epoch before a downgrade or
// configuration replacement. There may be durable work without a live process.
func (s *Store) SettleReviewChatWork(ctx context.Context, reviewID string, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "settle reviewer Chat epoch", func(q *gen.Queries) error {
		review, err := q.GetReviewByID(ctx, reviewID)
		if err != nil {
			return err
		}
		conversation, err := q.SelectConversationByReview(ctx, nullableString(reviewID))
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			if _, err := cleanupOwnedReviewControllerWork(ctx, q, reviewID, conversation.ID, review.ControllerGeneration, now); err != nil {
				return err
			}
		}
		_, err = q.ClearReviewChatController(ctx, gen.ClearReviewChatControllerParams{ID: reviewID, UpdatedAt: now})
		return err
	})
}
