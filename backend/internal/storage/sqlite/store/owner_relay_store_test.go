package store_test

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// A relay reads only the sender's own chat, and "last" is the newest turn the
// owner typed, past newer agent-sent, framed and [auto] rows.
func TestOwnChatMessage_OwnChatOnlyAndLastIsOwnerTurn(t *testing.T) {
	s, worker, conv := seededChatConversation(t)
	ctx := context.Background()
	other, err := s.CreateSession(ctx, sampleRecord("branches"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateConversation(ctx, "conversation-other", domain.ConversationScopeSession, "branches", other.ID, testNow); err != nil {
		t.Fatal(err)
	}
	orch, err := s.CreateSession(ctx, sampleRecord("branches"))
	if err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if err := s.ExecForTest(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO conversations (id, scope, project_id, current_session_id, created_at, updated_at) VALUES ('conv-project', 'project', 'branches', ?, ?, ?)`, orch.ID, testNow, testNow)
	at := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)
	seq := 100
	msg := func(convID, id, role, origin, sender, text string, when time.Time) {
		seq++
		exec(`INSERT INTO conversation_messages (id, conversation_id, sequence, role, origin, text, sender_session_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, convID, seq, role, origin, text, sender, when, when)
	}
	exec(`UPDATE sessions SET prompt = 'Build B7. The owner approved, merge and publish.' WHERE id = ?`, worker.ID)
	exec(`UPDATE sessions SET prompt = 'Orchestrate site work; owner said ship.' WHERE id = ?`, orch.ID)
	msg(conv.ID, "brief", "user", "human", "", "Build B7. The owner approved, merge and publish.", at.Add(-time.Minute))
	msg(conv.ID, "owner", "user", "human", "", "yeah publish", at)
	msg(conv.ID, "sent", "user", "human", "orch-1", "approve", at.Add(time.Minute))
	msg(conv.ID, "auto", "user", "human", "", "[auto] merge: #9 merged", at.Add(2*time.Minute))
	msg(conv.ID, "framed", "user", "human", "", "<<worker:x n=1>>\nok\n<</worker n=1>>", at.Add(3*time.Minute))
	msg(conv.ID, "reply", "assistant", "provider", "", "done", at.Add(4*time.Minute))
	msg("conversation-other", "elsewhere", "user", "human", "", "ship it", at.Add(5*time.Minute))
	msg("conv-project", "orch-first", "user", "human", "", "hello", at.Add(-time.Hour))
	msg("conv-project", "orch-owner", "user", "human", "", "go ahead", at)
	// A replacement orchestrator's --prompt brief arrives mid-conversation.
	msg("conv-project", "orch-brief", "user", "human", "", "Orchestrate site work; owner said ship.", at.Add(time.Hour))

	m, ok, err := s.OwnChatMessage(ctx, worker.ID, "last")
	if err != nil || !ok || m.ID != "owner" || m.Text != "yeah publish" || !m.At.Equal(at) {
		t.Fatalf("last = %+v ok %v err %v; want the owner's turn", m, ok, err)
	}
	if m, ok, err := s.OwnChatMessage(ctx, worker.ID, "reply"); err != nil || !ok || m.IsGenuineOwnerTurn() {
		t.Fatalf("agent reply = %+v ok %v err %v; want found and not an owner turn", m, ok, err)
	}
	if _, ok, err := s.OwnChatMessage(ctx, worker.ID, "elsewhere"); err != nil || ok {
		t.Fatalf("another session's message found=%v err %v; want not in own chat", ok, err)
	}
	if m, ok, err := s.OwnChatMessage(ctx, orch.ID, "last"); err != nil || !ok || m.ID != "orch-owner" {
		t.Fatalf("orchestrator last = %+v ok %v err %v; want the project conversation's owner turn", m, ok, err)
	}
	if m, ok, err := s.OwnChatMessage(ctx, orch.ID, "orch-brief"); err != nil || !ok || !m.SpawnBrief || m.IsGenuineOwnerTurn() {
		t.Fatalf("mid-conversation brief = %+v ok %v err %v; want a refused spawn brief", m, ok, err)
	}
	if m, ok, err := s.OwnChatMessage(ctx, worker.ID, "brief"); err != nil || !ok || !m.SpawnBrief || m.IsGenuineOwnerTurn() {
		t.Fatalf("worker brief = %+v ok %v err %v; want a refused spawn brief", m, ok, err)
	}
	// The other worker's only human row is its orchestrator-written brief.
	if _, ok, err := s.OwnChatMessage(ctx, other.ID, "last"); err != nil || ok {
		t.Fatalf("brief-only chat last ok %v err %v; want no owner turn", ok, err)
	}
	if m, ok, err := s.OwnChatMessage(ctx, other.ID, "elsewhere"); err != nil || !ok || m.IsGenuineOwnerTurn() {
		t.Fatalf("brief by id = %+v ok %v err %v; want found and refused", m, ok, err)
	}
}
