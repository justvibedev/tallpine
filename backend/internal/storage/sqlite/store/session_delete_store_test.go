package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// Deleting a session keeps its spend in the account usage view and its number
// out of later spawns.
func TestDeleteTerminatedSessionKeepsUsageAndNumber(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	t0 := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	session := seedUsageSession(t, s, domain.HarnessClaudeCode)
	source := seedUsageSource(t, s, session, t0)
	mustNoError(t, s.BindSessionAgentAccount(ctx, session.ID, domain.AgentAccountProviderClaude, "device-claude", t0))
	event := anthropicUsageEvent("e1", 10, 0, 0, 5)
	event.CreatedAt = t0.Add(time.Hour)
	mustNoError(t, s.ApplyUsageChunk(ctx, source.ID, 0, source.UpdatedAt, domain.SourceCursorState{
		ByteOffset: 100, State: domain.UsageSourceActive, ParserStateJSON: `{}`, UpdatedAt: t0,
	}, []domain.ModelUsageEvent{event}))

	if deleted, err := s.DeleteTerminatedSession(ctx, session.ID); err != nil || deleted {
		t.Fatalf("live delete = %v, %v; want refused", deleted, err)
	}
	session.IsTerminated = true
	mustNoError(t, s.UpdateSession(ctx, session))
	deleted, err := s.DeleteTerminatedSession(ctx, session.ID)
	if err != nil || !deleted {
		t.Fatalf("delete = %v, %v", deleted, err)
	}
	if _, ok, err := s.GetSession(ctx, session.ID); err != nil || ok {
		t.Fatalf("session still present: ok=%v err=%v", ok, err)
	}

	rows, err := s.AgentAccountUsageSince(ctx, t0.Truncate(24*time.Hour))
	mustNoError(t, err)
	if len(rows) != 1 || rows[0].AccountID != "device-claude" || rows[0].InputTokens != 10 || rows[0].OutputTokens != 5 || rows[0].Day != "2026-10-14" {
		t.Fatalf("usage after delete = %+v, want the deleted session's spend kept", rows)
	}

	next, err := s.CreateSession(ctx, sampleRecord("usage"))
	mustNoError(t, err)
	if next.ID == session.ID {
		t.Fatalf("next session reused deleted id %s", session.ID)
	}
}

func TestDeleteTerminatedSessionRefusesSharedHistory(t *testing.T) {
	s, session, conversation := seededChatConversation(t)
	ctx := context.Background()
	other := sampleRecord(string(session.ProjectID))
	other, err := s.CreateSession(ctx, other)
	mustNoError(t, err)
	seedTurn(t, s, conversation.ID, other.ID, "turn-other", "hello", time.Now().UTC())
	other.IsTerminated = true
	mustNoError(t, s.UpdateSession(ctx, other))
	if _, err := s.DeleteTerminatedSession(ctx, other.ID); !errors.Is(err, domain.ErrSessionHistoryShared) {
		t.Fatalf("err = %v, want ErrSessionHistoryShared", err)
	}
}
