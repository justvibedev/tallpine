package chat_test

import (
	"context"
	"sync/atomic"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
)

func TestModelPickOnStoppedAgentIsSavedWithoutResuming(t *testing.T) {
	t.Parallel()

	h, _ := settledHibernationHarness(t, domain.TurnStateCompleted)
	ctx := context.Background()
	if err := h.svc.Stop(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	var wakeCalls atomic.Int32
	h.svc.SetWakeCallback(func(context.Context, domain.SessionID) error {
		wakeCalls.Add(1)
		return nil
	})
	if _, err := h.svc.SetTurnSettings(ctx, testSession, domain.ConversationSettings{Model: "opus"}); err != nil {
		t.Fatalf("SetTurnSettings after stop = %v, want the pick saved", err)
	}
	snapshot, err := h.svc.Snapshot(ctx, testSession)
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshot.Conversation.Settings.Model; got != "opus" {
		t.Fatalf("stopped agent shows model %q, want opus", got)
	}
	if got := wakeCalls.Load(); got != 0 {
		t.Fatalf("saving a pick woke the agent %d times", got)
	}
}

// Stop, pick, start: the saved pick, not the caller's session default, is what the
// restarted agent runs with.
func TestStoppedModelPickIsAppliedOnNextStart(t *testing.T) {
	t.Parallel()

	var resumeCfg ports.ChatResumeConfig
	h, _ := hibernationHarnessWith(t, "", func(opts *chatsvc.Options) {
		opts.Drivers = fakeRegistry{driver: fakeDriver{conv: newFakeConversation(), resumeCfg: &resumeCfg}}
	})
	ctx := context.Background()
	if err := h.svc.Stop(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.SetTurnSettings(ctx, testSession, domain.ConversationSettings{Model: "opus"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Start(ctx, chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1", Model: "sonnet",
	}); err != nil {
		t.Fatalf("start after pick = %v", err)
	}
	if resumeCfg.Model != "opus" {
		t.Fatalf("restarted agent runs %q, want the saved pick opus", resumeCfg.Model)
	}
}
