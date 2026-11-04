package chat_test

import (
	"context"
	"fmt"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// B520: a turn the provider refused for authentication leaves the session
// reporting a required sign-in, not only waiting_input.
func TestServiceReportsReauthRequiredAfterAuthRefusal(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "first", ClientMessageID: "c1", Origin: domain.MessageOriginHuman})
	if err != nil {
		t.Fatal(err)
	}
	if h.svc.ReauthRequired(ctx, testSession) {
		t.Fatal("reported a reauth before any refusal")
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn.ProviderTurnID, TurnState: domain.TurnStateFailed,
		Err: fmt.Errorf("%w: ACP session/prompt: Authentication required", ports.ErrChatAuthRequired)})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return s.Conversation.Account != nil && s.Conversation.Account.ReauthRequiredAt != nil
	})
	if !h.svc.ReauthRequired(ctx, testSession) {
		t.Fatal("ReauthRequired = false after the provider refused the login")
	}
	if h.svc.ReauthRequired(ctx, "no-such-session") {
		t.Fatal("a session without a controller must report no reauth")
	}
}
