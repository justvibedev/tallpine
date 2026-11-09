package store_test

import (
	"context"
	"testing"
	"time"
)

func TestSessionAutoReviewOverrideRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	r, _ := s.CreateSession(ctx, sampleRecord("mer"))
	if _, found, err := s.SessionAutoReviewOverride(ctx, r.ID); err != nil || found {
		t.Fatalf("fresh session has no override: found=%v err=%v", found, err)
	}
	now := time.Now()
	for _, want := range []bool{false, true} {
		if ok, err := s.SetSessionAutoReview(ctx, r.ID, want, now); err != nil || !ok {
			t.Fatalf("set %v: ok=%v err=%v", want, ok, err)
		}
		if got, found, err := s.SessionAutoReviewOverride(ctx, r.ID); err != nil || !found || got != want {
			t.Fatalf("override = %v found=%v err=%v, want %v", got, found, err, want)
		}
	}
	if ok, err := s.SetSessionAutoReview(ctx, "mer-missing", true, now); err != nil || ok {
		t.Fatalf("missing session: ok=%v err=%v", ok, err)
	}
}

func TestSyncSessionAutoReviewFlagLeavesOverrideAlone(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	r, _ := s.CreateSession(ctx, sampleRecord("mer"))
	if ok, err := s.SyncSessionAutoReviewFlag(ctx, r.ID, true, time.Now()); err != nil || !ok {
		t.Fatalf("sync: ok=%v err=%v", ok, err)
	}
	got, _, _ := s.GetSession(ctx, r.ID)
	if !got.AutoReviewEnabled {
		t.Fatal("flag not synced")
	}
	if _, found, _ := s.SessionAutoReviewOverride(ctx, r.ID); found {
		t.Fatal("sync must not record an override")
	}
}
