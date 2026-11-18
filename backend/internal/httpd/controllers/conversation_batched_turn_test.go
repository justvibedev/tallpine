package controllers

import (
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
)

// B812: a queued message batched into another turn's input names that turn, so
// the transcript can show it inside the turn that answers it.
func TestConversationSnapshotExposesBatchedDeliveryTurn(t *testing.T) {
	now := time.Date(2026, 11, 17, 21, 11, 25, 0, time.UTC)
	out := conversationSnapshotResponse(chatsvc.Snapshot{Turns: []domain.ConversationTurn{
		{ID: "carrier", State: domain.TurnStateRunning, RequestedAt: now},
		{ID: "follower", State: domain.TurnStateCompleted, DeliveredInTurnID: "carrier", RequestedAt: now},
	}})
	if len(out.Turns) != 2 || out.Turns[0].DeliveredInTurnID != "" || out.Turns[1].DeliveredInTurnID != "carrier" {
		t.Fatalf("turns = %+v", out.Turns)
	}
}
