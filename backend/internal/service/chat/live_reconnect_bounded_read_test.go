package chat_test

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// B810: a live reconnect read the whole conversation before reattaching. On a
// long orchestrator history (6.2k turns, 11 branches) that read outlasted the
// 30 s recovery deadline on every attempt, so the session never got a
// controller back. The reconnect must not depend on the full snapshot: it reads
// only the running turns it restores.
func TestServiceLiveReconnectDoesNotLoadWholeConversation(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	var ids atomic.Int32
	newID := func() string { return fmt.Sprintf("bounded-%d", ids.Add(1)) }
	firstProvider := &terminatingConversation{fakeConversation: newFakeConversation()}
	first := chatsvc.New(chatsvc.Options{
		Store: st, Reader: fullSnapshotReader(st), Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: firstProvider}},
		Log:     slog.New(slog.DiscardHandler), NewID: newID,
	})
	firstController, err := first.Start(context.Background(), chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}
	turn, err := firstController.Send(context.Background(), ports.ChatUserMessage{Text: "keep working"})
	if err != nil {
		t.Fatalf("first Send: %v", err)
	}
	firstProvider.emit(ports.ChatEvent{
		Kind: ports.ChatEventTurnStarted, ProviderTurnID: turn.ProviderTurnID,
		ProviderConversationID: firstProvider.ProviderConversationID(),
	})
	h := &harness{st: st, ctrl: firstController}
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		for _, candidate := range s.Turns {
			if candidate.ID == turn.ID {
				return candidate.State == domain.TurnStateRunning
			}
		}
		return false
	})
	first.StopAll(context.Background())

	// The production failure: the full read never finishes inside the deadline.
	var fullReads atomic.Int32
	stalled := chatsvc.SnapshotReaderFunc(func(context.Context, string) (chatsvc.ConversationRows, error) {
		fullReads.Add(1)
		return chatsvc.ConversationRows{}, fmt.Errorf("select turns: %w", context.DeadlineExceeded)
	})
	secondProvider := &liveReconnectedConversation{nativeHistoryConversation: &nativeHistoryConversation{
		fakeConversation: newFakeConversation(),
	}}
	second := chatsvc.New(chatsvc.Options{
		Store: st, Reader: stalled, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: secondProvider}},
		Log:     slog.New(slog.DiscardHandler), NewID: newID,
	})
	t.Cleanup(func() { second.StopAll(context.Background()) })
	secondController, err := second.Start(context.Background(), chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(), ProviderConversationID: firstProvider.ProviderConversationID(),
		ControllerReady: func(result chatsvc.StartResult) (chatsvc.ControllerCommit, error) {
			if !result.LiveReconnect {
				t.Fatal("same live provider was reported as a fresh spawn")
			}
			return chatsvc.ControllerCommit{}, nil
		},
	})
	if err != nil {
		t.Fatalf("live reconnect failed on a stalled full-history read: %v", err)
	}
	if got := fullReads.Load(); got != 0 {
		t.Fatalf("live reconnect read the whole conversation %d times, want 0", got)
	}
	// The running turn is still restored from the bounded read.
	if got := secondController.State(); got != ports.ChatControllerBusy {
		t.Fatalf("controller state after reconnect = %q, want busy for the surviving turn", got)
	}
}
