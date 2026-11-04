package chat_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

// clearingConversation plays a Codex thread that can open a replacement thread.
type clearingConversation struct {
	*fakeConversation
	clears    int
	nextID    string
	commitErr error
}

func (c *clearingConversation) ClearContext(_ context.Context, commit func(string) error) error {
	c.clears++
	if err := commit(c.nextID); err != nil {
		c.commitErr = err
		return err
	}
	c.providerConversationID = c.nextID
	return nil
}

func newCodexClearHarness(t *testing.T) (*harness, *clearingConversation) {
	t.Helper()
	conv := &clearingConversation{fakeConversation: newFakeConversation(), nextID: "thread-2"}
	h := newHarnessWithConversationAndStoreForHarness(t, conv, func(st *sqlite.Store) chatsvc.Store { return st }, domain.HarnessCodex)
	// The harness seeds an unbound session; bind the session and its root branch
	// to the thread the fake plays, as a started production Chat is.
	db := fallbackTestDB(t, h.st)
	for _, q := range []string{
		`UPDATE sessions SET provider_conversation_id = 'thread-1' WHERE id = ?`,
		`UPDATE conversation_branches SET provider_conversation_id = 'thread-1' WHERE conversation_id IN (SELECT id FROM conversations WHERE current_session_id = ?)`,
	} {
		if _, err := db.Exec(q, string(testSession)); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	return h, conv
}

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

// /clear on Codex opens a new thread, binds the new id durably (session record
// and active branch, so reattach and restart restore use it), never forwards the
// text to the agent, and leaves a "Context cleared" marker in the transcript.
func TestCodexClearRebindsNativeIDAndEmitsMarker(t *testing.T) {
	h, conv := newCodexClearHarness(t)
	ctx := context.Background()
	before, branchBefore := boundIDs(t, h)
	if before != "thread-1" || branchBefore != "thread-1" {
		t.Fatalf("setup: bound to %q/%q, want thread-1", before, branchBefore)
	}

	turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: " /clear "})
	if err != nil {
		t.Fatalf("Send(/clear): %v", err)
	}
	if turn.ID != "" || turn.ContextClearedActivityID == "" {
		t.Fatalf("Send(/clear) turn = %+v, want no turn and the marker id", turn)
	}
	if conv.clears != 1 {
		t.Fatalf("ClearContext calls = %d, want 1", conv.clears)
	}
	if len(conv.sent) != 0 {
		t.Fatalf("/clear was forwarded to the agent as prompt text: %v", conv.sent)
	}
	if session, branch := boundIDs(t, h); session != "thread-2" || branch != "thread-2" {
		t.Fatalf("durably bound to session=%q branch=%q, want thread-2", session, branch)
	}
	agentID, providerID, ok := chatsvc.StartConfigOwnerIDs(h.svc, testSession)
	if !ok || agentID != "thread-2" || providerID != "thread-2" {
		t.Fatalf("retained start-config owner = %q/%q (found=%v), want thread-2: a stale fence breaks the next branch edit or reconnect", agentID, providerID, ok)
	}
	snapshot, err := h.st.LoadConversationSnapshot(ctx, h.ctrl.ConversationID())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, activity := range snapshot.Activities {
		found = found || (activity.Kind == domain.ActivityKindSystem && strings.Contains(activity.Summary, "Context cleared"))
	}
	if !found {
		t.Fatalf("no 'Context cleared' marker in %d activities", len(snapshot.Activities))
	}
}

func TestSteerPathsClearForCodex(t *testing.T) {
	h, conv := newCodexClearHarness(t)
	ctx := context.Background()
	if res, err := h.svc.Steer(ctx, testSession, ports.ChatUserMessage{Text: "/clear"}); err != nil || res.ActivityID == "" {
		t.Fatalf("Steer(/clear) = %+v, %v", res, err)
	}
	conv.nextID = "thread-3"
	res, err := h.svc.SteerOrSend(ctx, testSession, ports.ChatUserMessage{Text: "/clear", ClientMessageID: "c1"}, false)
	if err != nil || res.Steered || !res.Cleared || res.Steer.ActivityID == "" {
		t.Fatalf("SteerOrSend(/clear) = %+v, %v", res, err)
	}
	if conv.clears != 2 || len(conv.sent) != 0 {
		t.Fatalf("clears=%d sent=%v, want 2 clears and nothing forwarded", conv.clears, conv.sent)
	}
	if session, _ := boundIDs(t, h); session != "thread-3" {
		t.Fatalf("bound to %q, want thread-3", session)
	}
}

// Claude's ACP bridge handles /clear itself (verified), so it passes through
// unchanged: no new thread, the text reaches the agent.
func TestSendForwardsClearForClaude(t *testing.T) {
	h := newHarnessForHarness(t, domain.HarnessClaudeCode)
	if _, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{Text: "/clear"}); errors.Is(err, chatsvc.ErrClearUnsupported) {
		t.Fatalf("Send(/clear) refused for claude: %v", err)
	}
	h.conv.mu.Lock()
	defer h.conv.mu.Unlock()
	if len(h.conv.sent) != 1 || h.conv.sent[0].Text != "/clear" {
		t.Fatalf("claude /clear was not forwarded verbatim: %v", h.conv.sent)
	}
}

// A clear swaps the thread, so it must wait for a running turn: it is refused,
// nothing is cleared, and the durable id stays put.
func TestCodexClearRefusedWhileTurnRunning(t *testing.T) {
	h, conv := newCodexClearHarness(t)
	ctx := context.Background()
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "work"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "/clear"}); !errors.Is(err, chatsvc.ErrClearWhileBusy) {
		t.Fatalf("Send(/clear) = %v, want ErrClearWhileBusy", err)
	}
	if _, err := h.svc.SteerOrSend(ctx, testSession, ports.ChatUserMessage{Text: "/clear", ClientMessageID: "c9"}, false); !errors.Is(err, chatsvc.ErrClearWhileBusy) {
		t.Fatalf("SteerOrSend(/clear) = %v, want ErrClearWhileBusy", err)
	}
	if conv.clears != 0 {
		t.Fatalf("ClearContext ran %d times under a running turn", conv.clears)
	}
	if session, branch := boundIDs(t, h); session != "thread-1" || branch != "thread-1" {
		t.Fatalf("ids moved to %q/%q despite the refusal", session, branch)
	}
}
