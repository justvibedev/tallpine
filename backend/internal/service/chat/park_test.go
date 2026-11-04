package chat_test

import (
	"context"
	"log/slog"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// A deliberate quit stops an idle agent the way hibernation does, and the
// next daemon resumes the same native conversation on the next message.
func TestParkAllSleepsIdleAgentAndNextDaemonResumesIt(t *testing.T) {
	t.Parallel()

	h, old := settledHibernationHarness(t, domain.TurnStateCompleted, func() bool { return false })
	ctx := context.Background()
	h.svc.ParkAll(ctx)
	if !h.svc.Parking() || old.calls.Load() != 1 || h.svc.HasLiveChatController(testSession) || !asleep(t, h) {
		t.Fatalf("after ParkAll: parking=%v stops=%d live=%v asleep=%v",
			h.svc.Parking(), old.calls.Load(), h.svc.HasLiveChatController(testSession), asleep(t, h))
	}
	// Nothing wakes an agent while the daemon quits: the message waits.
	if turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "sent during quit", ClientMessageID: "quit-1"}); err != nil || turn.State != domain.TurnStateQueued {
		t.Fatalf("send during quit = %+v, %v", turn, err)
	}
	if err := h.svc.RetryQueuedWakes(ctx); err != nil || h.svc.HasLiveChatController(testSession) {
		t.Fatalf("parking daemon woke an agent: err=%v", err)
	}

	resumed := newFakeConversation()
	resumed.turnSeq = 1
	var resumeConfig ports.ChatResumeConfig
	next := chatsvc.New(chatsvc.Options{
		Store: h.st, Reader: fullSnapshotReader(h.st), Sessions: h.st,
		Drivers:  fakeRegistry{driver: fakeDriver{conv: resumed, resumeCfg: &resumeConfig}},
		Activity: h.activity, Log: slog.New(slog.DiscardHandler), Now: h.now,
		NewID: func() string { return "next-daemon" },
	})
	t.Cleanup(func() { _ = next.Stop(context.Background(), testSession) })
	var attempts atomic.Int32
	next.SetWakeCallback(wakeAs(h, next, old.ProviderConversationID(), &attempts, func(int32) bool { return false }))
	if err := next.RetryQueuedWakes(ctx); err != nil {
		t.Fatal(err)
	}
	h.awaitSnapshot(t, func(store.ConversationSnapshot) bool { return len(resumed.sentTexts()) == 1 })
	if got := resumed.sentTexts(); !slices.Equal(got, []string{"sent during quit"}) {
		t.Fatalf("resumed agent received %v", got)
	}
	if resumeConfig.ProviderConversationID != old.ProviderConversationID() {
		t.Fatalf("resumed native conversation %q, want %q", resumeConfig.ProviderConversationID, old.ProviderConversationID())
	}
	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(s.Turns) == 2 })
	if snapshot.Turns[0].State != domain.TurnStateCompleted {
		t.Fatalf("earlier turn after resume = %s, want the conversation preserved", snapshot.Turns[0].State)
	}
}

// interruptingConversation settles its running turn when interrupted, as a
// provider does.
type interruptingConversation struct {
	*hibernationConversation
	interrupts atomic.Int32
}

func (c *interruptingConversation) Interrupt(context.Context, string) error {
	c.interrupts.Add(1)
	go c.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", TurnState: domain.TurnStateCancelled})
	return nil
}

func runningTurnHarness(t *testing.T, conv ports.ChatConversation, fake *fakeConversation) *harness {
	t.Helper()
	st := openStore(t)
	h := &harness{st: st, conv: fake, activity: &recordingActivity{}, clock: time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)}
	h.svc = chatsvc.New(chatsvc.Options{
		Store: st, Reader: fullSnapshotReader(st), Sessions: st,
		Drivers:  fakeRegistry{driver: fakeDriver{conv: conv}},
		Activity: h.activity, Log: slog.New(slog.DiscardHandler), Now: h.now,
		NewID: func() string { return "park-" + time.Now().Format("150405.000000000") },
	})
	ctx := context.Background()
	ctrl, err := h.svc.Start(ctx, chatsvc.StartConfig{SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex, WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	h.ctrl = ctrl
	t.Cleanup(func() { _ = h.svc.Stop(context.Background(), testSession) })
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "long build", ClientMessageID: "long-1"}); err != nil {
		t.Fatal(err)
	}
	fake.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateRunning
	})
	rec, _, err := st.GetSession(ctx, testSession)
	if err != nil {
		t.Fatal(err)
	}
	rec.Metadata.ProviderConversationID = "native-1"
	if err := st.UpdateSession(ctx, rec); err != nil {
		t.Fatal(err)
	}
	return h
}

func TestParkAllInterruptsARunningTurnBeforeStopping(t *testing.T) {
	t.Parallel()

	conv := &interruptingConversation{hibernationConversation: &hibernationConversation{fakeConversation: newFakeConversation()}}
	h := runningTurnHarness(t, conv, conv.fakeConversation)
	h.svc.ParkAll(context.Background())
	if conv.interrupts.Load() == 0 {
		t.Fatal("running turn was not interrupted before stopping")
	}
	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(s.Turns) == 1 })
	if state := snapshot.Turns[0].State; state != domain.TurnStateCancelled {
		t.Fatalf("interrupted turn = %s, want cancelled", state)
	}
	if conv.calls.Load() != 1 || h.svc.HasLiveChatController(testSession) || !asleep(t, h) {
		t.Fatalf("after ParkAll: hibernate calls=%d live=%v asleep=%v", conv.calls.Load(), h.svc.HasLiveChatController(testSession), asleep(t, h))
	}
}

func TestParkAllClosesOutATurnThatWillNotSettle(t *testing.T) {
	t.Parallel()

	conv := &hibernationConversation{fakeConversation: newFakeConversation()}
	h := runningTurnHarness(t, conv, conv.fakeConversation)
	h.svc.ParkAll(context.Background())
	if h.svc.HasLiveChatController(testSession) || !asleep(t, h) {
		t.Fatalf("after ParkAll: live=%v asleep=%v", h.svc.HasLiveChatController(testSession), asleep(t, h))
	}
	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State != domain.TurnStateRunning
	})
	if state := snapshot.Turns[0].State; state == domain.TurnStateRunning || state == domain.TurnStateCompleted {
		t.Fatalf("unsettled turn after quit = %s, want closed out as not finished", state)
	}
}

// interruptedConversation answers an interrupt like the ACP driver: the turn
// settles as interrupted.
type interruptedConversation struct{ *interruptingConversation }

func (c *interruptedConversation) Interrupt(context.Context, string) error {
	c.interrupts.Add(1)
	go c.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", TurnState: domain.TurnStateInterrupted})
	return nil
}

func settledError(t *testing.T, h *harness) string {
	t.Helper()
	snap := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateInterrupted
	})
	return snap.Turns[0].ErrorMessage
}

func TestParkAllLabelsTheInterruptedTurnAsAnAppRestart(t *testing.T) {
	t.Parallel()
	inner := &interruptingConversation{hibernationConversation: &hibernationConversation{fakeConversation: newFakeConversation()}}
	h := runningTurnHarness(t, &interruptedConversation{inner}, inner.fakeConversation)
	h.svc.ParkAll(context.Background())
	if got := settledError(t, h); got != ports.ErrChatAppRestart.Error() {
		t.Fatalf("quit-interrupted turn error = %q, want the app-restart marker", got)
	}
}

func TestUserStopStaysAnUnlabeledInterrupt(t *testing.T) {
	t.Parallel()
	inner := &interruptingConversation{hibernationConversation: &hibernationConversation{fakeConversation: newFakeConversation()}}
	h := runningTurnHarness(t, &interruptedConversation{inner}, inner.fakeConversation)
	if err := h.svc.Interrupt(context.Background(), testSession); err != nil {
		t.Fatal(err)
	}
	if got := settledError(t, h); got != "" {
		t.Fatalf("user-stopped turn error = %q, want none", got)
	}
}
