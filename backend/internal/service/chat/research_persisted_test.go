package chat_test

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
)

// B736: research mode persisted on the session comes back when the chat
// controller starts (as after a daemon restart), and a finished turn moves the
// persisted quiet-hour clock through the controller's turn-end hook.
func TestPersistedResearchIsRestoredAtStartAndTurnsMoveItsClock(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := openStore(t)
	orch, found, err := st.GetSession(ctx, testSession)
	if err != nil || !found {
		t.Fatalf("fixture: found=%v err=%v", found, err)
	}
	orch.Kind, orch.Harness = domain.KindOrchestrator, domain.HarnessClaudeCode
	if err = st.UpdateSession(ctx, orch); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)
	since := now.Add(-30 * time.Minute)
	if err = st.SetSessionResearch(ctx, testSession, &domain.SessionResearch{Window: "400k", Since: since, LastTurnAt: since}); err != nil {
		t.Fatal(err)
	}
	conv := newFakeConversation()
	var nextID atomic.Int32
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Reader: fullSnapshotReader(st), Sessions: st, ResearchStore: st,
		Drivers:  fakeRegistry{driver: fakeDriver{conv: conv}},
		Activity: &recordingActivity{}, Log: slog.New(slog.DiscardHandler),
		Now:   func() time.Time { return now },
		NewID: func() string { return fmt.Sprintf("research-%d", nextID.Add(1)) },
	})
	if _, err = svc.Start(ctx, chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Kind: domain.KindOrchestrator,
		Harness: domain.HarnessClaudeCode, WorkspacePath: t.TempDir(),
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })
	status, ok := svc.ResearchStatus(ctx, testSession)
	if !ok || status.ResearchWindow != "400k" || !status.Since.Equal(since) {
		t.Fatalf("restored status %+v ok %v, want 400k since %v", status, ok, since)
	}

	if _, err = svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "work", ClientMessageID: uuid.NewString(), Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatal(err)
	}
	const provider, thread = "provider-turn-1", "thread-1"
	conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: provider, ProviderConversationID: thread})
	conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: provider, ProviderConversationID: thread, TurnState: domain.TurnStateCompleted})
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		row, found, err := st.SessionResearch(ctx, testSession)
		if err != nil || !found {
			t.Fatalf("persisted research: found=%v err=%v", found, err)
		}
		if row.LastTurnAt.Equal(now) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("persisted last turn %v, want the turn's %v", row.LastTurnAt, now)
		}
	}
}
