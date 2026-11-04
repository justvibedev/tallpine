package store

import (
	"context"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// QueuedDeliveryMessage is an internal delivery read. Unlike the human-only
// QueuedTurnMessage edit boundary it also reads attributed automation prompts.
func (s *Store) QueuedDeliveryMessage(ctx context.Context, conversationID, turnID string) (domain.ConversationMessage, error) {
	row, err := s.qr.SelectQueuedDeliveryMessage(ctx, gen.SelectQueuedDeliveryMessageParams{
		ConversationID: conversationID, TurnID: nullableString(turnID),
	})
	if err != nil {
		return domain.ConversationMessage{}, err
	}
	return messageToDomain(row), nil
}

// QueuedMessages reads a bounded prefix of the dispatch queue in FIFO order.
func (s *Store) QueuedMessages(ctx context.Context, conversationID string, limit int) ([]domain.ConversationMessage, error) {
	order, err := s.qr.SelectQueuedConversationTurnOrder(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	var messages []domain.ConversationMessage
	for _, turn := range order[:min(len(order), max(0, limit))] {
		msg, err := s.QueuedDeliveryMessage(ctx, conversationID, turn.ID)
		if err != nil {
			return nil, err
		}
		messages = append(messages, msg)
	}
	return messages, nil
}

// CombineQueuedMessages moves a contiguous queue prefix into its first prompt.
// All source rows remain durable and point to the turn that now carries them.
// This reuses the same absorption link as queued-turn promotion. Both prompt
// replacement and source retirement commit together, before provider dispatch.
func (s *Store) CombineQueuedMessages(ctx context.Context, conversationID string, messages []domain.ConversationMessage, text string, now time.Time) error {
	if len(messages) == 0 {
		return ErrQueuedTurnNotAvailable
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "combine queued messages", func(q *gen.Queries) error {
		order, err := q.SelectQueuedConversationTurnOrder(ctx, conversationID)
		if err != nil {
			return err
		}
		if len(order) < len(messages) {
			return ErrQueuedTurnNotAvailable
		}
		for i, expected := range messages {
			if order[i].ID != expected.TurnID {
				return ErrQueuedTurnNotAvailable
			}
			current, err := q.SelectQueuedDeliveryMessage(ctx, gen.SelectQueuedDeliveryMessageParams{
				ConversationID: conversationID, TurnID: nullableString(expected.TurnID),
			})
			if err != nil {
				return err
			}
			if current.ID != expected.ID || current.Revision != expected.Revision {
				return ErrQueuedTurnNotAvailable
			}
		}
		first := messages[0]
		changed, err := q.UpdateQueuedConversationMessageText(ctx, gen.UpdateQueuedConversationMessageTextParams{
			ConversationID: conversationID, TurnID: nullableString(first.TurnID),
			Revision: first.Revision, Text: text, DeliveryContentJson: first.DeliveryContentJSON, UpdatedAt: now,
		})
		if err != nil {
			return err
		}
		if changed != 1 {
			return ErrQueuedTurnNotAvailable
		}
		for _, source := range messages[1:] {
			changed, err = q.ReserveQueuedConversationTurnForPromotion(ctx, gen.ReserveQueuedConversationTurnForPromotionParams{
				ID: source.TurnID, ConversationID: conversationID, PromotionStartedAt: nullTime(now),
			})
			if err != nil {
				return err
			}
			if changed != 1 {
				return ErrQueuedTurnNotAvailable
			}
			changed, err = q.CompleteQueuedConversationTurnPromotion(ctx, gen.CompleteQueuedConversationTurnPromotionParams{
				ID: source.TurnID, ConversationID: conversationID, CompletedAt: nullTime(now), PromotedToTurnID: nullableString(first.TurnID),
			})
			if err != nil {
				return err
			}
			if changed != 1 {
				return ErrQueuedTurnNotAvailable
			}
		}
		return nil
	})
}
