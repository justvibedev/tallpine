package store_test

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

// B667: an owner message queued behind a busy orchestrator while it hands off
// stays in the project conversation, still handled by the retired session. The
// successor's first controller rebinds the conversation and drains that message
// before its own brief; binding it must succeed under the successor's
// generation, and the brief must follow, or the successor's start fails.
func TestRebindAdoptsMessageQueuedForRetiredOrchestrator(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "handoff-chat")

	oldRecord := sampleRecord("handoff-chat")
	oldRecord.Kind = domain.KindOrchestrator
	oldRecord.Mode = domain.SessionModeChat
	retired, err := s.CreateSession(ctx, oldRecord)
	if err != nil {
		t.Fatalf("create retiring orchestrator: %v", err)
	}
	project, err := s.CreateConversation(ctx, "project-row", domain.ConversationScopeProject,
		"handoff-chat", retired.ID, histClock)
	if err != nil {
		t.Fatalf("create project conversation: %v", err)
	}
	if err := s.ClaimChatControllerGeneration(ctx, retired.ID, "gen-old"); err != nil {
		t.Fatal(err)
	}
	// The retiring orchestrator is busy running its handoff call...
	if _, err := s.AppendUserMessage(ctx, project.ID, retired.ID, "gen-old", domain.ConversationMessage{
		ID: "busy-message", Text: "write the note and hand off", Origin: domain.MessageOriginHuman, ClientMessageID: "client-busy",
	}, "busy-turn", histClock); err != nil {
		t.Fatalf("seed running turn: %v", err)
	}
	if err := s.BindTurnToProvider(ctx, "busy-turn", "provider-busy", histClock); err != nil {
		t.Fatal(err)
	}
	// ...when the owner's next message queues behind it.
	if _, err := s.AppendUserMessage(ctx, project.ID, retired.ID, "gen-old", domain.ConversationMessage{
		ID: "owner-message", Text: "also check the build", Origin: domain.MessageOriginHuman, ClientMessageID: "client-owner",
	}, "owner-turn", histClock.Add(time.Second)); err != nil {
		t.Fatalf("queue owner message: %v", err)
	}
	if err := s.SettleTurnByID(ctx, "busy-turn", domain.TurnStateCompleted, "", histClock.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	retired.IsTerminated = true
	if err := s.UpdateSession(ctx, retired); err != nil {
		t.Fatalf("retire orchestrator: %v", err)
	}

	// The successor provisions with the handoff brief queued in its own row.
	newRecord := sampleRecord("handoff-chat")
	newRecord.Kind = domain.KindOrchestrator
	newRecord.Mode = domain.SessionModeChat
	newRecord.ProvisionState = domain.SessionProvisionProvisioning
	successor, err := s.CreateSession(ctx, newRecord)
	if err != nil {
		t.Fatalf("create successor: %v", err)
	}
	provisioning, err := s.CreateConversation(ctx, "provisioning-row", domain.ConversationScopeSession,
		"handoff-chat", successor.ID, histClock.Add(3*time.Second))
	if err != nil {
		t.Fatalf("create provisioning conversation: %v", err)
	}
	if _, err := s.AppendUserMessage(ctx, provisioning.ID, successor.ID, "", domain.ConversationMessage{
		ID: "brief-message", Text: "Read the note and continue.", Origin: domain.MessageOriginHuman, ClientMessageID: "client-brief",
	}, "brief-turn", histClock.Add(3*time.Second)); err != nil {
		t.Fatalf("queue brief: %v", err)
	}

	reset := domain.ConversationActivity{
		ID: "reset-activity", Kind: domain.ActivityKindSystem, Status: domain.ActivityStatusCompleted,
		Summary: "Agent context reset.", Detail: []byte(`{"event":"context.reset"}`),
		ProviderItemID: domain.ConversationContextResetProviderItemID(successor.ID),
	}
	rebound, err := s.CreateProjectConversationAdoptingQueue(ctx, "unused", "handoff-chat", successor.ID, reset, histClock.Add(4*time.Second))
	if err != nil {
		t.Fatalf("rebind project conversation: %v", err)
	}
	if rebound.ID != project.ID {
		t.Fatalf("rebound conversation = %s, want %s", rebound.ID, project.ID)
	}
	// The successor's timeline starts at its context boundary; the prompt it is
	// about to answer must be shown after it, ahead of the brief.
	page, err := s.LoadConversationSnapshotPage(ctx, project.ID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	var visible []string
	for _, message := range page.Messages {
		visible = append(visible, message.ID)
	}
	if len(visible) != 2 || visible[0] != "owner-message" || visible[1] != "brief-message" {
		t.Fatalf("successor timeline = %v, want [owner-message brief-message]", visible)
	}
	if err := s.ClaimChatControllerGeneration(ctx, successor.ID, "gen-new"); err != nil {
		t.Fatal(err)
	}

	for i, want := range []string{"owner-turn", "brief-turn"} {
		batch, err := s.ClaimQueuedBatch(ctx, project.ID, true, 1, histClock.Add(time.Duration(5+i)*time.Second))
		if err != nil || len(batch) != 1 || batch[0].TurnID != want {
			t.Fatalf("drain %d = %+v, %v; want %s", i, batch, err, want)
		}
		if err := s.BindPrimaryChatTurnToProvider(ctx, want, "provider-"+want, "gen-new", histClock.Add(time.Duration(5+i)*time.Second)); err != nil {
			t.Fatalf("bind %s under the successor: %v", want, err)
		}
		turn, err := s.TurnByID(ctx, want)
		if err != nil {
			t.Fatal(err)
		}
		if turn.HandledBySessionID != successor.ID {
			t.Fatalf("%s handled by %s, want successor %s", want, turn.HandledBySessionID, successor.ID)
		}
		if err := s.SettleTurnByID(ctx, want, domain.TurnStateCompleted, "", histClock.Add(time.Duration(5+i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	// History the retired orchestrator ran stays attributed to it.
	busy, err := s.TurnByID(ctx, "busy-turn")
	if err != nil {
		t.Fatal(err)
	}
	if busy.HandledBySessionID != retired.ID {
		t.Fatalf("completed turn moved to %s, want it kept on %s", busy.HandledBySessionID, retired.ID)
	}
}

// seedSettledHistoryTurn records an earlier orchestrator's completed prompt
// ("old-turn"/"old-message"). It is settled on purpose: a prompt still queued
// when the project conversation is rebound moves to the new owner (B667).
func seedSettledHistoryTurn(t *testing.T, s *sqlite.Store, conversationID string, session domain.SessionID, generation string, at time.Time) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.AppendUserMessage(ctx, conversationID, session, generation, domain.ConversationMessage{
		ID: "old-message", Text: "old orchestrator history", Origin: domain.MessageOriginHuman,
	}, "old-turn", at); err != nil {
		t.Fatalf("append old message: %v", err)
	}
	if err := s.SettleTurnByID(ctx, "old-turn", domain.TurnStateCompleted, "", at); err != nil {
		t.Fatalf("settle old turn: %v", err)
	}
}
