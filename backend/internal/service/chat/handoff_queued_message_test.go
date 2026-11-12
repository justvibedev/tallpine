package chat_test

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

// B667: the owner sends a message while the orchestrator is busy running its
// own handoff, so it queues in the project conversation for the retiring
// session. The successor's first controller inherits that conversation and must
// deliver the queued message and then its handoff brief. Before the fix the
// drain failed with "primary Chat turn owner changed before bind", the
// successor's start failed, and the project was left without an orchestrator.
func TestSuccessorDrainsMessageQueuedDuringHandoff(t *testing.T) {
	t.Parallel()

	st, id := openProvisioningStore(t, domain.SessionProvisionProvisioning)
	ctx := context.Background()
	provisioningOrchestrator(t, st, id)
	now := time.Now().UTC().Add(-time.Minute) // the retiring session's rows predate the brief

	retiring, err := st.CreateSession(ctx, domain.SessionRecord{
		ProjectID: testProject, Kind: domain.KindOrchestrator,
		Harness: domain.HarnessCodex, Mode: domain.SessionModeChat,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("seed retiring orchestrator: %v", err)
	}
	project, err := st.CreateConversation(ctx, "project-narrative",
		domain.ConversationScopeProject, testProject, retiring.ID, now)
	if err != nil {
		t.Fatalf("seed project conversation: %v", err)
	}
	if err := st.ClaimChatControllerGeneration(ctx, retiring.ID, "retiring-gen"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendUserMessage(ctx, project.ID, retiring.ID, "retiring-gen", domain.ConversationMessage{
		ID: "handoff-request", Text: "hand off now", Origin: domain.MessageOriginHuman, ClientMessageID: "client-handoff",
	}, "handoff-turn", now); err != nil {
		t.Fatalf("seed running handoff turn: %v", err)
	}
	if err := st.BindTurnToProvider(ctx, "handoff-turn", "provider-handoff", now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendUserMessage(ctx, project.ID, retiring.ID, "retiring-gen", domain.ConversationMessage{
		ID: "owner-message", Text: "owner message sent during handoff", Origin: domain.MessageOriginHuman,
		ClientMessageID: "client-owner",
	}, "owner-turn", now.Add(time.Second)); err != nil {
		t.Fatalf("queue owner message behind the busy controller: %v", err)
	}
	if err := st.SettleTurnByID(ctx, "handoff-turn", domain.TurnStateCompleted, "", now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	retiring.IsTerminated = true
	if err := st.UpdateSession(ctx, retiring); err != nil {
		t.Fatalf("retire orchestrator: %v", err)
	}

	svc := provisioningService(t, st)
	if _, err := svc.Send(ctx, id, ports.ChatUserMessage{
		Text: "Read the note and continue.", Origin: domain.MessageOriginHuman, ClientMessageID: "client-brief",
	}); err != nil {
		t.Fatalf("queue handoff brief: %v", err)
	}
	startOrchestratorController(t, st, svc, id)
	if err := svc.DrainQueued(ctx, id); err != nil {
		t.Fatalf("successor's first drain: %v", err)
	}

	snapshot, err := svc.Snapshot(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Conversation.ID != project.ID {
		t.Fatalf("successor conversation = %s, want inherited %s", snapshot.Conversation.ID, project.ID)
	}
	owner, ok := findTurn(snapshot.Turns, "owner-turn")
	if !ok || owner.State != domain.TurnStateRunning || owner.HandledBySessionID != id {
		t.Fatalf("owner message = %+v (found %v), want running on successor %s", owner, ok, id)
	}
	brief := 0
	for _, turn := range snapshot.Turns {
		if turn.ID != "owner-turn" && turn.ID != "handoff-turn" {
			brief++
			if turn.State != domain.TurnStateQueued || turn.HandledBySessionID != id {
				t.Fatalf("handoff brief = %+v, want queued on successor %s behind the owner message", turn, id)
			}
		}
	}
	if brief != 1 {
		t.Fatalf("turns = %+v, want the handoff brief queued once", snapshot.Turns)
	}
}

func findTurn(turns []domain.ConversationTurn, id string) (domain.ConversationTurn, bool) {
	for _, turn := range turns {
		if turn.ID == id {
			return turn, true
		}
	}
	return domain.ConversationTurn{}, false
}

// seedSettledHistoryTurn records an earlier orchestrator's completed prompt
// ("old-turn"/"old-message"). It is settled on purpose: a prompt still queued
// when the project conversation is rebound moves to the new owner (B667).
func seedSettledHistoryTurn(t *testing.T, st *sqlite.Store, conversationID string, session domain.SessionID, generation string, at time.Time) {
	t.Helper()
	ctx := context.Background()
	if _, err := st.AppendUserMessage(ctx, conversationID, session, generation, domain.ConversationMessage{
		ID: "old-message", Text: "old orchestrator history", Origin: domain.MessageOriginHuman,
	}, "old-turn", at); err != nil {
		t.Fatalf("seed old history: %v", err)
	}
	if err := st.SettleTurnByID(ctx, "old-turn", domain.TurnStateCompleted, "", at); err != nil {
		t.Fatalf("settle old history: %v", err)
	}
}
