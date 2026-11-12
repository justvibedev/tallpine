package chat_test

import (
	"context"
	"errors"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
)

// boundIDs reads the provider id restore would resume from: the session record
// and the conversation's active branch.
func boundIDs(t *testing.T, h *harness) (session, branch string) {
	t.Helper()
	ctx := context.Background()
	record, found, err := h.st.GetSession(ctx, testSession)
	if err != nil || !found {
		t.Fatalf("GetSession: found=%v err=%v", found, err)
	}
	conversation, err := h.st.ConversationForSession(ctx, testSession)
	if err != nil {
		t.Fatalf("ConversationForSession: %v", err)
	}
	b, err := h.st.ConversationBranch(ctx, conversation.ID, conversation.ActiveBranchID)
	if err != nil {
		t.Fatalf("ConversationBranch: %v", err)
	}
	return record.Metadata.ProviderConversationID, b.ProviderConversationID
}

// Only the bare command clears. A "/clear" later in a Claude message is prose:
// it reaches Claude verbatim and nothing is cleared.
func TestClaudeForwardsClearInsideAMessage(t *testing.T) {
	h := newHarnessForHarness(t, domain.HarnessClaudeCode)
	const text = "please check /clear handling"
	turn, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{Text: text})
	if err != nil {
		t.Fatalf("Send(%q): %v", text, err)
	}
	if turn.ContextClearedActivityID != "" {
		t.Fatalf("a message mentioning /clear cleared the context")
	}
	h.conv.mu.Lock()
	defer h.conv.mu.Unlock()
	if len(h.conv.sent) != 1 || h.conv.sent[0].Text != text {
		t.Fatalf("message was not forwarded verbatim: %v", h.conv.sent)
	}
}

// "/clear" followed by more text would run as Claude Code's own /clear with
// arguments: a native clear that drops the brief. Every delivery path refuses
// it, and none forwards it.
func TestClaudeRefusesClearWithMoreText(t *testing.T) {
	h := newHarnessForHarness(t, domain.HarnessClaudeCode)
	ctx := context.Background()
	msg := ports.ChatUserMessage{Text: "/clear and start over", ClientMessageID: "c1"}
	if _, err := h.svc.Send(ctx, testSession, msg); !errors.Is(err, chatsvc.ErrClearWithText) {
		t.Fatalf("Send = %v, want ErrClearWithText", err)
	}
	if _, err := h.svc.Steer(ctx, testSession, msg); !errors.Is(err, chatsvc.ErrClearWithText) {
		t.Fatalf("Steer = %v, want ErrClearWithText", err)
	}
	if _, err := h.svc.SteerOrSend(ctx, testSession, msg, false); !errors.Is(err, chatsvc.ErrClearWithText) {
		t.Fatalf("SteerOrSend = %v, want ErrClearWithText", err)
	}
	h.conv.mu.Lock()
	defer h.conv.mu.Unlock()
	if len(h.conv.sent) != 0 {
		t.Fatalf("a /clear with more text reached Claude: %v", h.conv.sent)
	}
}

// Codex reads a message that opens with "/clear" as prompt text, so only the
// bare command is Tallpine's; the rest reaches Codex verbatim.
func TestCodexForwardsClearWithMoreText(t *testing.T) {
	h := newHarnessForHarness(t, domain.HarnessCodex)
	const text = "/clear and start over"
	turn, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{Text: text})
	if err != nil {
		t.Fatalf("Send(%q): %v", text, err)
	}
	if turn.ContextClearedActivityID != "" {
		t.Fatalf("a message opening with /clear cleared the Codex context")
	}
	h.conv.mu.Lock()
	defer h.conv.mu.Unlock()
	if len(h.conv.sent) != 1 || h.conv.sent[0].Text != text {
		t.Fatalf("message was not forwarded verbatim: %v", h.conv.sent)
	}
}
