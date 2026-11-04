package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// RecordProviderInput saves the exact combined text for native replay. A normal
// send records it before provider I/O; an accepted steer records it afterward.
func (s *Store) RecordProviderInput(ctx context.Context, turnID, text string) error {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	rows, err := q.SetConversationProviderInputText(ctx, gen.SetConversationProviderInputTextParams{
		ProviderInputText: text, ID: turnID,
	})
	return expectBatchRow("record provider input", turnID, rows, err)
}

func expectBatchRow(action, id string, rows int64, err error) error {
	if err != nil {
		return fmt.Errorf("%s %s: %w", action, id, err)
	}
	if rows != 1 {
		return fmt.Errorf("%s %s: expected one row, got %d", action, id, rows)
	}
	return nil
}

// ListQueuedBatch previews the current queue without reserving any message.
func (s *Store) ListQueuedBatch(ctx context.Context, conversationID string, limit int) ([]domain.QueuedTurn, error) {
	rows, err := s.qr.SelectQueuedConversationBatch(ctx, conversationID)
	if err != nil {
		return nil, fmt.Errorf("list queued batch: %w", err)
	}
	rows = batchablePrefix(rows, true)
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	if len(rows) == 0 {
		return nil, domain.ErrNoQueuedTurn
	}
	batch := make([]domain.QueuedTurn, 0, len(rows))
	for _, row := range rows {
		batch = append(batch, domain.QueuedTurn{
			TurnID: row.ID, Text: row.Text, ClientMessageID: row.ClientMessageID,
			Origin: row.Origin, DeliveryContentJSON: row.DeliveryContentJson,
			SenderSessionID: row.SenderSessionID, SenderProjectID: row.SenderProjectID,
			SenderDisplayName: row.SenderDisplayName,
		})
	}
	return batch, nil
}

// ClaimQueuedBatch fixes one delivery boundary before provider I/O. The first
// row becomes the provider turn for a normal drain. Other rows are reserved as
// members of that input. A steer reserves every row without starting a new turn.
func (s *Store) ClaimQueuedBatch(ctx context.Context, conversationID string, dispatch bool, limit int, now time.Time) ([]domain.QueuedTurn, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var batch []domain.QueuedTurn
	err := s.inTx(ctx, "claim queued batch", func(q *gen.Queries) error {
		rows, err := q.SelectQueuedConversationBatch(ctx, conversationID)
		if err != nil {
			return err
		}
		rows = batchablePrefix(rows, dispatch)
		if limit > 0 && len(rows) > limit {
			rows = rows[:limit]
		}
		for _, row := range rows {
			changed, err := q.ReserveQueuedConversationTurnForPromotion(ctx, gen.ReserveQueuedConversationTurnForPromotionParams{
				PromotionStartedAt: sql.NullTime{Time: now, Valid: true}, ID: row.ID, ConversationID: conversationID,
			})
			if checkErr := expectBatchRow("reserve queued batch turn", row.ID, changed, err); checkErr != nil {
				return checkErr
			}
			batch = append(batch, domain.QueuedTurn{
				TurnID: row.ID, Text: row.Text, ClientMessageID: row.ClientMessageID,
				Origin: row.Origin, DeliveryContentJSON: row.DeliveryContentJson,
				SenderSessionID: row.SenderSessionID, SenderProjectID: row.SenderProjectID,
				SenderDisplayName: row.SenderDisplayName,
			})
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("claim queued batch: %w", err)
	}
	if len(batch) == 0 {
		return nil, domain.ErrNoQueuedTurn
	}
	return batch, nil
}

// CompleteQueuedBatch links each source receipt to the provider turn that took
// its input. The original messages keep their own transcript metadata.
func (s *Store) CompleteQueuedBatch(ctx context.Context, conversationID string, turnIDs []string, providerTurnID, mode string, now time.Time) error {
	if len(turnIDs) == 0 {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "complete queued batch", func(q *gen.Queries) error {
		target, err := q.SelectConversationTurnByProviderID(ctx, gen.SelectConversationTurnByProviderIDParams{
			ConversationID: conversationID, ProviderTurnID: providerTurnID,
		})
		if err != nil {
			return err
		}
		for _, id := range turnIDs {
			changed, err := q.CompleteQueuedConversationBatchMember(ctx, gen.CompleteQueuedConversationBatchMemberParams{
				CompletedAt:      sql.NullTime{Time: now, Valid: true},
				PromotedToTurnID: sql.NullString{String: target.ID, Valid: true},
				DeliveryMode:     mode,
				ID:               id, ConversationID: conversationID,
			})
			if checkErr := expectBatchRow("complete queued batch turn", id, changed, err); checkErr != nil {
				return checkErr
			}
		}
		return nil
	})
}

// ReleaseQueuedBatch returns a definitively refused steer batch to its queue.
func (s *Store) ReleaseQueuedBatch(ctx context.Context, conversationID string, turnIDs []string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "release queued batch", func(q *gen.Queries) error {
		for _, id := range turnIDs {
			changed, err := q.ReleaseQueuedConversationTurnPromotion(ctx, gen.ReleaseQueuedConversationTurnPromotionParams{ID: id, ConversationID: conversationID})
			if checkErr := expectBatchRow("release queued batch turn", id, changed, err); checkErr != nil {
				return checkErr
			}
		}
		return nil
	})
}

// FailQueuedBatch retains uncertain source messages without replaying them.
func (s *Store) FailQueuedBatch(ctx context.Context, conversationID string, turnIDs []string, reason string, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "fail queued batch", func(q *gen.Queries) error {
		for _, id := range turnIDs {
			if err := q.SettleConversationTurn(ctx, gen.SettleConversationTurnParams{
				State: domain.TurnStateFailed, ErrorMessage: reason,
				CompletedAt: sql.NullTime{Time: now, Valid: true}, ID: id,
			}); err != nil {
				return fmt.Errorf("fail queued batch turn %s: %w", id, err)
			}
		}
		return nil
	})
}

// FailReservedQueuedBatch closes stranded reservations during live reconnect.
func (s *Store) FailReservedQueuedBatch(ctx context.Context, conversationID string, now time.Time) error {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	return q.FailReservedQueuedConversationTurns(ctx, gen.FailReservedQueuedConversationTurnsParams{
		CompletedAt: sql.NullTime{Time: now, Valid: true}, ConversationID: conversationID,
	})
}

// batchablePrefix keeps the queue boundaries Chat already guarantees. A
// protected head (worker send or report) is delivered alone, never merged with
// what follows, and a steer never absorbs one. Any other batch stops before the
// first protected message so nothing is reordered.
func batchablePrefix(rows []gen.SelectQueuedConversationBatchRow, dispatch bool) []gen.SelectQueuedConversationBatchRow {
	n := 0
	for n < len(rows) && !domain.QueueBoundaryProtected(rows[n].ClientMessageID) {
		n++
	}
	if n == 0 && dispatch && len(rows) > 0 {
		return rows[:1]
	}
	return rows[:n]
}
