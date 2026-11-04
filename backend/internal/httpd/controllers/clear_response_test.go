package controllers

import (
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// A /clear creates no turn, so it must not read as a duplicate send.
func TestSendResponseReportsClearNotDuplicate(t *testing.T) {
	got := sendConversationMessageResponse(domain.ConversationTurn{ContextClearedActivityID: "act-1"})
	if !got.Cleared || got.Duplicate || got.ActivityID != "act-1" {
		t.Fatalf("response = %+v, want cleared with the marker and not duplicate", got)
	}
	if dup := sendConversationMessageResponse(domain.ConversationTurn{}); !dup.Duplicate || dup.Cleared {
		t.Fatalf("empty turn = %+v, want duplicate", dup)
	}
}
