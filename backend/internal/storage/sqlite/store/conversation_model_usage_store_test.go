package store_test

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

func TestModelUsageAccumulatesAcrossTurnsAndSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	s, err := sqlite.Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	seedProject(t, s, "hist")
	rec := sampleRecord("hist")
	rec.Mode = domain.SessionModeChat
	session, err := s.CreateSession(ctx, rec)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	conv, err := s.CreateConversation(ctx, "conv-1", domain.ConversationScopeSession, "hist", session.ID, histClock)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	now := time.Now().UTC()
	turn1 := []domain.ModelUsage{
		{Model: "claude-sonnet-5-5", InputTokens: 10, CacheCreationTokens: 100, CacheReadTokens: 1000, OutputTokens: 5},
		{Model: "claude-haiku-5-5", InputTokens: 1, OutputTokens: 2, Credits: 0.25},
	}
	turn2 := []domain.ModelUsage{{Model: "claude-sonnet-5-5", InputTokens: 20, CacheCreationTokens: 200, CacheReadTokens: 2000, OutputTokens: 7}, {Model: "claude-haiku-5-5", Credits: 0.5}}
	for _, turn := range [][]domain.ModelUsage{turn1, turn2} {
		if err := s.AddModelUsage(ctx, conv.ID, turn, now); err != nil {
			t.Fatalf("AddModelUsage: %v", err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	s, err = sqlite.Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	got, err := s.ModelUsageForSession(ctx, session.ID)
	if err != nil {
		t.Fatalf("ModelUsageForSession: %v", err)
	}
	want := []domain.ModelUsage{
		{Model: "claude-haiku-5-5", InputTokens: 1, OutputTokens: 2, Credits: 0.75, Turns: 2},
		{Model: "claude-sonnet-5-5", InputTokens: 30, CacheCreationTokens: 300, CacheReadTokens: 3000, OutputTokens: 12, Turns: 2},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("usage = %+v, want %+v", got, want)
	}
}
