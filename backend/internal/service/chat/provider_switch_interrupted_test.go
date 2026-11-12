package chat_test

import (
	"context"
	"errors"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// B673: a Claude /clear (a provider switch to the same agent) binds a fresh
// provider id, and its first turn is stopped by a daemon restart before the
// provider saved anything. That stopped turn is not an answer: the resume must
// still be allowed to start fresh when the provider reports the id missing,
// instead of failing on every start with "could not resume".
func TestResumeStartsAnInterruptedSwitchBranchFresh(t *testing.T) {
	t.Parallel()

	h, _, _ := newEditHarness(t, false)
	ctx := context.Background()
	if err := h.svc.SwitchProvider(ctx, testSession, domain.ProviderSwitchRequest{TargetHarness: domain.HarnessClaudeCode}); err != nil {
		t.Fatalf("SwitchProvider: %v", err)
	}
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "first after clear", ClientMessageID: "c-first"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	// The restart ends the controller before the turn completes.
	if err := h.svc.Stop(ctx, testSession); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		state, ok := turnStateByText(t, s)["first after clear"]
		return ok && state != domain.TurnStateRunning && state != domain.TurnStateQueued
	})
	if state := turnStateByText(t, snapshot)["first after clear"]; state == domain.TurnStateCompleted {
		t.Fatalf("turn state = %q; the test needs a turn the stop cut short", state)
	}
	record, found, err := h.st.GetSession(ctx, testSession)
	if err != nil || !found {
		t.Fatalf("GetSession: found=%v err=%v", found, err)
	}
	var resumed ports.ChatResumeConfig
	driver := fakeDriver{conv: newFakeConversation()}
	driver.resume = func(cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
		resumed = cfg
		if !cfg.FreshIfMissing {
			return nil, errors.New("provider conversation not found")
		}
		conv := newFakeConversation()
		conv.providerConversationID = "thread-started-fresh"
		return conv, nil
	}
	restarted := newRestartedEditService(t, h, driver, "interrupted-branch-restart")
	if _, err := restarted.Start(ctx, chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Kind: domain.KindWorker,
		Harness: domain.HarnessClaudeCode, WorkspacePath: t.TempDir(),
		ProviderConversationID: record.Metadata.ProviderConversationID,
	}); err != nil {
		t.Fatalf("Start after an interrupted first turn on a switch target: %v", err)
	}
	if !resumed.FreshIfMissing {
		t.Fatal("resume after an interrupted first turn did not allow a fresh start")
	}
	conversation, err := h.st.ConversationForSession(ctx, testSession)
	if err != nil {
		t.Fatalf("ConversationForSession: %v", err)
	}
	branch, err := h.st.ConversationBranch(ctx, conversation.ID, conversation.ActiveBranchID)
	if err != nil || branch.ProviderConversationID != "thread-started-fresh" {
		t.Fatalf("active branch provider = %q, %v; want it moved to the fresh conversation", branch.ProviderConversationID, err)
	}
}
