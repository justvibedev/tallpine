package chat_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
)

// B667 on the /clear handoff (B659): unlike an orchestrator handoff, a clear
// keeps the session and does not rebind the project conversation. An owner
// message sent after the source stopped and before the fresh context starts is
// refused as retryable (ErrControllerHandoff, HTTP CHAT_INTERFACE_TRANSITION)
// rather than queued under the stopped controller, so nothing is stranded; the
// same message sent again reaches the fresh context, and no turn fails.
func TestClaudeClearRefusesMidHandoffSendAsRetryable(t *testing.T) {
	t.Parallel()
	h, driver := newClearHarness(t, domain.HarnessClaudeCode)
	ctx := context.Background()
	const owner = "owner message sent during the clear"
	sent := make(chan error, 1)
	driver.mu.Lock()
	driver.beforeStart = func(ports.ChatStartConfig) {
		go func() {
			_, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
				Text: owner, Origin: domain.MessageOriginHuman, ClientMessageID: "client-during-clear",
			})
			sent <- err
		}()
		time.Sleep(50 * time.Millisecond) // let the send land while the target is not up yet
	}
	driver.mu.Unlock()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "/clear", Origin: domain.MessageOriginHuman}); err != nil {
		t.Fatalf("Send(/clear): %v", err)
	}
	select {
	case err := <-sent:
		if !errors.Is(err, chatsvc.ErrControllerHandoff) {
			t.Fatalf("owner message during the clear = %v, want the retryable ErrControllerHandoff", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("owner message during the clear never returned")
	}
	awaitChatIdle(t, h)
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: owner, Origin: domain.MessageOriginHuman, ClientMessageID: "client-during-clear",
	}); err != nil {
		t.Fatalf("owner message sent again after the clear: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		driver.mu.Lock()
		fresh := driver.fresh
		driver.mu.Unlock()
		fresh.mu.Lock()
		delivered := false
		for _, message := range fresh.sent {
			delivered = delivered || strings.Contains(message.Text, owner)
		}
		fresh.mu.Unlock()
		if delivered {
			conversation, err := h.st.ConversationForSession(ctx, testSession)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := h.st.LoadConversationSnapshot(ctx, conversation.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, turn := range snapshot.Turns {
				if turn.State == domain.TurnStateFailed {
					t.Fatalf("a turn failed across the clear: %+v", turn)
				}
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the owner message sent during the clear never reached the fresh context")
}
