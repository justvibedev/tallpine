package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// NextQueuedTurn returns the oldest message recorded while the agent was busy,
// or ErrNoQueuedTurn when the queue is empty.
//
// The queue is these rows, not a slice in a controller: a message the user typed
// is durable before it is delivered, so a daemon that dies with one queued can
// still account for it.
func (s *Store) NextQueuedTurn(ctx context.Context, conversationID string) (domain.QueuedTurn, error) {
	row, err := s.qr.SelectNextQueuedConversationTurn(ctx, conversationID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.QueuedTurn{}, ErrNoQueuedTurn
	}
	if err != nil {
		return domain.QueuedTurn{}, fmt.Errorf("select next queued turn for %s: %w", conversationID, err)
	}
	return domain.QueuedTurn{
		TurnID:              row.ID,
		Text:                row.Text,
		ClientMessageID:     row.ClientMessageID,
		Origin:              row.Origin,
		DeliveryContentJSON: row.DeliveryContentJson,
		SenderSessionID:     row.SenderSessionID,
		SenderProjectID:     row.SenderProjectID,
		SenderDisplayName:   row.SenderDisplayName,
	}, nil
}

// ReserveQueuedTurnForPromotion atomically removes one selected queued turn from
// automatic drain and returns the durable content that must be steered.
func (s *Store) ReserveQueuedTurnForPromotion(
	ctx context.Context,
	conversationID, turnID string,
	now time.Time,
) (domain.QueuedTurn, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.ReserveQueuedConversationTurnForPromotion(ctx,
		gen.ReserveQueuedConversationTurnForPromotionParams{
			PromotionStartedAt: sql.NullTime{Time: now, Valid: true},
			ID:                 turnID, ConversationID: conversationID,
		})
	if err != nil {
		return domain.QueuedTurn{}, fmt.Errorf("reserve queued turn %s: %w", turnID, err)
	}
	if rows == 0 {
		return domain.QueuedTurn{}, fmt.Errorf("%w: %s", ErrQueuedTurnNotAvailable, turnID)
	}
	row, err := s.qw.SelectReservedConversationTurnForPromotion(ctx,
		gen.SelectReservedConversationTurnForPromotionParams{ID: turnID, ConversationID: conversationID})
	if err != nil {
		// Do not strand a reservation when its message row is corrupt or absent.
		_, _ = s.qw.ReleaseQueuedConversationTurnPromotion(ctx,
			gen.ReleaseQueuedConversationTurnPromotionParams{ID: turnID, ConversationID: conversationID})
		return domain.QueuedTurn{}, fmt.Errorf("load reserved queued turn %s: %w", turnID, err)
	}
	return domain.QueuedTurn{
		TurnID: row.ID, Text: row.Text, ClientMessageID: row.ClientMessageID,
		Origin: row.Origin, DeliveryContentJSON: row.DeliveryContentJson,
	}, nil
}

// ReleaseQueuedTurnPromotion restores a provider-refused reservation without
// changing its immutable queue order.
func (s *Store) ReleaseQueuedTurnPromotion(
	ctx context.Context,
	conversationID, turnID string,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.ReleaseQueuedConversationTurnPromotion(ctx,
		gen.ReleaseQueuedConversationTurnPromotionParams{ID: turnID, ConversationID: conversationID})
	if err != nil {
		return fmt.Errorf("release queued turn promotion %s: %w", turnID, err)
	}
	if rows == 0 {
		return fmt.Errorf("%w: %s", ErrQueuedTurnNotAvailable, turnID)
	}
	return nil
}

// CompleteQueuedTurnPromotion records the visible steer and retires its queued
// source together, so snapshots can never show both copies or neither copy.
func (s *Store) CompleteQueuedTurnPromotion(
	ctx context.Context,
	conversationID, sourceTurnID, providerTurnID string,
	activity domain.ConversationActivity,
	now time.Time,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "complete queued turn promotion", func(q *gen.Queries) error {
		target, err := q.SelectConversationTurnByProviderID(ctx,
			gen.SelectConversationTurnByProviderIDParams{
				ConversationID: conversationID, ProviderTurnID: providerTurnID,
			})
		if err != nil {
			return fmt.Errorf("select promotion target %s: %w", providerTurnID, err)
		}
		txCtx := context.WithValue(ctx, conversationProjectionTxKey{}, q)
		if err := s.UpsertActivity(txCtx, conversationID, providerTurnID, activity, now); err != nil {
			return err
		}
		rows, err := q.CompleteQueuedConversationTurnPromotion(ctx,
			gen.CompleteQueuedConversationTurnPromotionParams{
				CompletedAt:      sql.NullTime{Time: now, Valid: true},
				PromotedToTurnID: sql.NullString{String: target.ID, Valid: true},
				ID:               sourceTurnID,
				ConversationID:   conversationID,
			})
		if err != nil {
			return fmt.Errorf("complete source turn %s: %w", sourceTurnID, err)
		}
		if rows == 0 {
			return fmt.Errorf("%w: %s", ErrQueuedTurnNotAvailable, sourceTurnID)
		}
		return nil
	})
}

// CancelQueuedTurns closes out everything queued at or before cutoff.
//
// They settle as interrupted rather than failed: nothing went wrong, the user
// stopped the agent, and a message that was never dispatched did not fail. The
// cutoff keeps a message typed after the stop out of a cancellation it predates.
func (s *Store) CancelQueuedTurns(
	ctx context.Context,
	conversationID string,
	cutoff, now time.Time,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.CancelQueuedConversationTurns(ctx, gen.CancelQueuedConversationTurnsParams{
		CompletedAt:    sql.NullTime{Time: now, Valid: true},
		ConversationID: conversationID,
		RequestedAt:    cutoff,
	}); err != nil {
		return fmt.Errorf("cancel queued turns for %s: %w", conversationID, err)
	}
	return nil
}

// CancelAllQueuedTurns closes the whole durable queue after an interface
// handoff has synchronously fenced intake and promotion. Unlike an ordinary Stop
// action, there is no post-request message to preserve with a timestamp cutoff.
func (s *Store) CancelAllQueuedTurns(
	ctx context.Context,
	conversationID string,
	now time.Time,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.CancelAllQueuedConversationTurns(ctx, gen.CancelAllQueuedConversationTurnsParams{
		CompletedAt:    sql.NullTime{Time: now, Valid: true},
		ConversationID: conversationID,
	}); err != nil {
		return fmt.Errorf("cancel all queued turns for %s: %w", conversationID, err)
	}
	return nil
}

// CancelQueuedTurnByID removes one undispatched queue item without disturbing
// later queue items or the running turn.
func (s *Store) CancelQueuedTurnByID(
	ctx context.Context,
	conversationID, turnID string,
	now time.Time,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.CancelQueuedConversationTurnByID(ctx,
		gen.CancelQueuedConversationTurnByIDParams{
			CompletedAt:    sql.NullTime{Time: now, Valid: true},
			ID:             turnID,
			ConversationID: conversationID,
		})
	if err != nil {
		return fmt.Errorf("cancel queued turn %s: %w", turnID, err)
	}
	if rows == 0 {
		return fmt.Errorf("%w: %s", ErrQueuedTurnNotAvailable, turnID)
	}
	return nil
}

// ErrInvalidQueuedTurnOrder means the requested queue order does not match the
// current undispatched queue exactly.
var ErrInvalidQueuedTurnOrder = errors.New("invalid queued turn order")

// ReorderQueuedTurns permutes the durable queue order by reassigning existing
// requested_at values. The caller must pass every currently queued turn id in
// the desired FIFO order.
func (s *Store) ReorderQueuedTurns(
	ctx context.Context,
	conversationID string,
	turnIDs []string,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	current, err := s.qr.SelectQueuedConversationTurnOrder(ctx, conversationID)
	if err != nil {
		return fmt.Errorf("list queued turns for reorder: %w", err)
	}
	if len(current) != len(turnIDs) {
		return fmt.Errorf("%w: got %d ids for %d queued turns", ErrInvalidQueuedTurnOrder, len(turnIDs), len(current))
	}
	if len(current) == 0 {
		return nil
	}

	currentByID := make(map[string]time.Time, len(current))
	for _, row := range current {
		currentByID[row.ID] = row.RequestedAt
	}
	seen := make(map[string]struct{}, len(turnIDs))
	for _, turnID := range turnIDs {
		if _, ok := currentByID[turnID]; !ok {
			return fmt.Errorf("%w: unknown turn %s", ErrInvalidQueuedTurnOrder, turnID)
		}
		if _, dup := seen[turnID]; dup {
			return fmt.Errorf("%w: duplicate turn %s", ErrInvalidQueuedTurnOrder, turnID)
		}
		seen[turnID] = struct{}{}
	}

	requestedAts := make([]time.Time, len(current))
	for i, row := range current {
		requestedAts[i] = row.RequestedAt
	}
	sort.Slice(requestedAts, func(i, j int) bool {
		return requestedAts[i].Before(requestedAts[j])
	})

	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin queued turn reorder: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	q := s.qw.WithTx(tx)
	for i, turnID := range turnIDs {
		rows, err := q.UpdateQueuedConversationTurnRequestedAt(ctx,
			gen.UpdateQueuedConversationTurnRequestedAtParams{
				RequestedAt:    requestedAts[i],
				ID:             turnID,
				ConversationID: conversationID,
			})
		if err != nil {
			return fmt.Errorf("reorder queued turn %s: %w", turnID, err)
		}
		if rows == 0 {
			return fmt.Errorf("%w: %s", ErrQueuedTurnNotAvailable, turnID)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit queued turn reorder: %w", err)
	}
	return nil
}

// QueuedTurnMessage reads the durable text and content without exposing image
// bytes to the renderer.
func (s *Store) QueuedTurnMessage(ctx context.Context, conversationID, turnID string) (domain.ConversationMessage, error) {
	row, err := s.qr.SelectQueuedConversationMessage(ctx, gen.SelectQueuedConversationMessageParams{
		ConversationID: conversationID, TurnID: nullableString(turnID),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ConversationMessage{}, ErrQueuedTurnNotAvailable
	}
	if err != nil {
		return domain.ConversationMessage{}, err
	}
	return messageToDomain(row), nil
}

// UpdateQueuedTurnMessage commits the prompt and its retry receipt atomically.
func (s *Store) UpdateQueuedTurnMessage(
	ctx context.Context,
	conversationID, turnID, text, contentJSON string,
	revision int64,
	now time.Time,
	delivery domain.ConversationQueuedEditDelivery,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "update queued turn message", func(q *gen.Queries) error {
		if delivery.ClientMessageID != "" {
			hash, err := q.SelectConversationQueuedEditDelivery(ctx, gen.SelectConversationQueuedEditDeliveryParams{
				ConversationID: conversationID, ClientMessageID: delivery.ClientMessageID,
			})
			if err == nil {
				if hash != delivery.RequestHash {
					return ErrQueuedEditDeliveryConflict
				}
				return nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		rows, err := q.UpdateQueuedConversationMessageText(ctx,
			gen.UpdateQueuedConversationMessageTextParams{
				Text: text, DeliveryContentJson: contentJSON, Revision: revision,
				UpdatedAt: now, ConversationID: conversationID,
				TurnID: sql.NullString{String: turnID, Valid: true},
			})
		if err != nil {
			return fmt.Errorf("update queued turn message %s: %w", turnID, err)
		}
		if rows == 0 {
			return fmt.Errorf("%w: %s", ErrQueuedTurnNotAvailable, turnID)
		}
		if delivery.ClientMessageID != "" {
			return q.InsertConversationQueuedEditDelivery(ctx, gen.InsertConversationQueuedEditDeliveryParams{
				ConversationID: conversationID, ClientMessageID: delivery.ClientMessageID,
				RequestHash: delivery.RequestHash, CreatedAt: now,
			})
		}
		return nil
	})
}

// HasOpenTurns reports whether the conversation has a turn that is queued,
// reserved for promotion, or still running: a request the agent has not
// answered yet.
func (s *Store) HasOpenTurns(ctx context.Context, conversationID string) (bool, error) {
	var n int
	err := s.readDB.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM conversation_turns WHERE conversation_id = ? AND state IN ('queued', 'running'))`,
		conversationID).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("check open turns for %s: %w", conversationID, err)
	}
	return n == 1, nil
}
