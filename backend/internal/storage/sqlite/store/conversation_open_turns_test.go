package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// B494: a turn reserved for promotion no longer shows as "next queued", but
// the worker still owes an answer, so it counts as open.
func TestHasOpenTurnsCoversReservedTurn(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	if open, err := s.HasOpenTurns(ctx, conversation); err != nil || open {
		t.Fatalf("empty conversation open=%v err=%v, want false", open, err)
	}
	if created, err := s.AppendUserMessage(ctx, conversation, session, "gen-1",
		domain.ConversationMessage{ID: "m-1", Text: "what rotated the token?", Origin: domain.MessageOriginAutomation},
		"t-1", histClock); err != nil || !created {
		t.Fatalf("append: created=%v err=%v", created, err)
	}
	if _, err := s.ReserveQueuedTurnForPromotion(ctx, conversation, "t-1", histClock.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.NextQueuedTurn(ctx, conversation); !errors.Is(err, domain.ErrNoQueuedTurn) {
		t.Fatalf("next queued err=%v, want none", err)
	}
	if open, err := s.HasOpenTurns(ctx, conversation); err != nil || !open {
		t.Fatalf("reserved turn open=%v err=%v, want true", open, err)
	}
}
