package chat_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

type failingPendingCheckStore struct{ *sqlite.Store }

func (failingPendingCheckStore) HasPendingConversationInteractions(context.Context, string) (bool, error) {
	return false, errors.New("store unavailable")
}

// A store error while checking for other pending approvals must not leave the
// session waiting-input until the turn ends: the resolution is still reported.
func TestApprovalResolvedReportsActiveWhenPendingCheckFails(t *testing.T) {
	t.Parallel()

	h := newHarnessWithConversationAndStore(t, nil, func(st *sqlite.Store) chatsvc.Store { return failingPendingCheckStore{Store: st} })
	if _, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{Text: "go", ClientMessageID: "c1"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventApprovalResolved, RequestID: "approval-1"})
	deadline := time.Now().Add(5 * time.Second)
	for !hasActivitySignal(h.activity.snapshot(), domain.ActivityActive, "chat.approval.resolved") {
		if time.Now().After(deadline) {
			t.Fatal("no active resolution signal after the pending-interaction check failed")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
