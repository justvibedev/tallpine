package store_test

import (
	"context"
	"testing"
	"time"
)

// Agent messages are stored as role user / origin human with a sender session
// id; only a message without one is the owner's.
func TestBacklogPlanOwnerMessages_ExcludeAgentSent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "p1")
	at := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	exec := func(q string, args ...any) {
		t.Helper()
		if err := s.ExecForTest(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO conversations (id, scope, project_id, created_at, updated_at) VALUES ('c1', 'project', 'p1', ?, ?)`, at, at)
	msg := func(id string, seq int, sender string, when time.Time) {
		exec(`INSERT INTO conversation_messages (id, conversation_id, sequence, role, origin, text, sender_session_id, created_at, updated_at)
VALUES (?, 'c1', ?, 'user', 'human', 'x', ?, ?, ?)`, id, seq, sender, when, when)
	}
	msg("owner", 1, "", at)
	msg("agent", 2, "worker-1", at.Add(time.Hour)) // newer, but sent by an agent

	id, _, ok, err := s.LatestHumanProjectMessage(ctx, "p1")
	if err != nil || !ok || id != "owner" {
		t.Fatalf("latest = %q ok %v err %v; want the owner's message, not the newer agent one", id, ok, err)
	}
	if src, ok, err := s.BacklogPlanMessageSource(ctx, "owner"); err != nil || !ok || !src.Human || src.ProjectID != "p1" {
		t.Fatalf("owner source = %+v %v %v", src, ok, err)
	}
	if src, ok, err := s.BacklogPlanMessageSource(ctx, "agent"); err != nil || !ok || src.Human {
		t.Fatalf("agent source = %+v %v %v; want not human", src, ok, err)
	}
}
