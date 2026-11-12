package chat_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/lifecycle"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
)

// An orchestrator whose saved conversation is gone starts a fresh one with a
// seed note (B678). The note joins the fresh agent's system prompt and stays on
// its new provider boundary, so every later restart of that agent carries it.
func TestContextResetNoteSeedsTheFreshProviderBoundary(t *testing.T) {
	t.Parallel()
	const note = "# Orchestrator recovery (automatic)"
	st := openStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	before, _ := seedProjectConversationWithProviderHistory(t, st, "lost-conversation", now)
	session, err := st.CreateSession(ctx, domain.SessionRecord{
		ProjectID: testProject, Kind: domain.KindOrchestrator, Harness: domain.HarnessCodex,
		Mode: domain.SessionModeChat, CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	fresh := newFakeConversation()
	fresh.providerConversationID = "fresh-thread"
	var started ports.ChatStartConfig
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st, Log: slog.New(slog.DiscardHandler),
		Drivers: fakeRegistry{driver: fakeDriver{conv: fresh, startCfg: &started}},
		NewID:   func() string { return "reset-boundary" },
		Now:     func() time.Time { return now.Add(2 * time.Second) },
	})
	marks := lifecycle.New(st, nil)
	if _, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: session.ID, ProjectID: testProject, Kind: domain.KindOrchestrator,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(),
		SystemPrompt: "standing instructions", ContextResetNote: note,
		ControllerReady: func(result chatsvc.StartResult) (chatsvc.ControllerCommit, error) {
			if result.ProviderBoundary == nil {
				return chatsvc.ControllerCommit{}, errors.New("no fresh provider boundary")
			}
			if err := marks.MarkChatSpawned(ctx, session.ID, domain.SessionMetadata{
				ProviderConversationID: result.ProviderConversationID, ControllerGeneration: result.ControllerGeneration,
			}, *result.ProviderBoundary); err != nil {
				return chatsvc.ControllerCommit{}, err
			}
			committed := result.Conversation
			committed.ActiveBranchID = result.ProviderBoundary.ID
			return chatsvc.ControllerCommit{Conversation: committed}, nil
		},
	}); err != nil {
		t.Fatalf("fresh start: %v", err)
	}
	if !strings.Contains(started.SystemPrompt, "standing instructions") || !strings.Contains(started.SystemPrompt, note) {
		t.Fatalf("fresh system prompt = %q, want the standing instructions and the note", started.SystemPrompt)
	}
	after, err := st.ConversationForSession(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	branch, err := st.ConversationBranch(ctx, after.ID, after.ActiveBranchID)
	if err != nil {
		t.Fatal(err)
	}
	if branch.ParentBranchID != before.ActiveBranchID || branch.ProviderConversationID != "fresh-thread" || branch.HandoffNote != note {
		t.Fatalf("fresh boundary = %+v, want a child of %q on fresh-thread holding the note", branch, before.ActiveBranchID)
	}
	if err := svc.Stop(ctx, session.ID); err != nil {
		t.Fatal(err)
	}

	resumedConv := newFakeConversation()
	resumedConv.providerConversationID = "fresh-thread"
	var resumed ports.ChatResumeConfig
	restarted := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st, Log: slog.New(slog.DiscardHandler),
		Drivers: fakeRegistry{driver: fakeDriver{conv: resumedConv, resumeCfg: &resumed}},
		NewID:   func() string { return "resume-generation" },
		Now:     func() time.Time { return now.Add(3 * time.Second) },
	})
	t.Cleanup(func() { _ = restarted.Stop(context.Background(), session.ID) })
	if _, err := restarted.Start(ctx, chatsvc.StartConfig{
		SessionID: session.ID, ProjectID: testProject, Kind: domain.KindOrchestrator,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(), SystemPrompt: "standing instructions",
		ProviderConversationID: "fresh-thread", HistoryMode: ports.ChatHistoryDeferred,
	}); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if !strings.Contains(resumed.SystemPrompt, note) {
		t.Fatalf("resumed system prompt = %q, want the boundary's note", resumed.SystemPrompt)
	}
}
