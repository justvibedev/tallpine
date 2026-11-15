package store_test

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestSessionResearchRoundTripTouchAndClear(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	r, _ := s.CreateSession(ctx, sampleRecord("mer"))
	if _, found, err := s.SessionResearch(ctx, r.ID); err != nil || found {
		t.Fatalf("fresh session: found=%v err=%v", found, err)
	}
	since := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	if err := s.SetSessionResearch(ctx, r.ID, &domain.SessionResearch{Window: "400k", Since: since, LastTurnAt: since}); err != nil {
		t.Fatal(err)
	}
	before, _, _ := s.GetSession(ctx, r.ID)
	turn := since.Add(20 * time.Minute)
	if err := s.TouchSessionResearch(ctx, r.ID, turn); err != nil {
		t.Fatal(err)
	}
	got, found, err := s.SessionResearch(ctx, r.ID)
	if err != nil || !found || got.Window != "400k" || !got.Since.Equal(since) || !got.LastTurnAt.Equal(turn) {
		t.Fatalf("research = %+v found=%v err=%v", got, found, err)
	}
	if after, _, _ := s.GetSession(ctx, r.ID); after.Revision <= before.Revision {
		t.Fatalf("revision %d -> %d, want bumped", before.Revision, after.Revision)
	}
	if err := s.SetSessionResearch(ctx, r.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := s.SessionResearch(ctx, r.ID); found {
		t.Fatal("cleared research mode still found")
	}
	// A turn after research mode ended does not bring it back.
	if err := s.TouchSessionResearch(ctx, r.ID, turn); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := s.SessionResearch(ctx, r.ID); found {
		t.Fatal("a touch revived research mode")
	}
}

func TestClearAllSessionResearch(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	a, _ := s.CreateSession(ctx, sampleRecord("mer"))
	b, _ := s.CreateSession(ctx, sampleRecord("mer"))
	now := time.Now().UTC()
	for _, id := range []domain.SessionID{a.ID, b.ID} {
		if err := s.SetSessionResearch(ctx, id, &domain.SessionResearch{Window: "250k", Since: now, LastTurnAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ClearAllSessionResearch(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []domain.SessionID{a.ID, b.ID} {
		if _, found, _ := s.SessionResearch(ctx, id); found {
			t.Fatalf("%s: research mode survived the clear", id)
		}
	}
}
