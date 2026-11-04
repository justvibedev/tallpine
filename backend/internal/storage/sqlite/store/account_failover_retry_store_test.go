package store_test

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestAccountFailoverRetryRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	t0 := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	first := domain.AccountFailoverRetry{TurnID: "t1", SessionID: "s1", MoveID: "m1", CreatedAt: t0}
	second := domain.AccountFailoverRetry{TurnID: "t2", SessionID: "s2", MoveID: "m2", CreatedAt: t0.Add(time.Minute)}
	mustNoError(t, s.SaveAccountFailoverRetry(ctx, second))
	mustNoError(t, s.SaveAccountFailoverRetry(ctx, first))

	// An update replaces the row and keeps its first created_at.
	first.Moved, first.Attempts, first.RetryAt, first.CreatedAt = true, 2, t0.Add(time.Hour), t0.Add(time.Hour)
	mustNoError(t, s.SaveAccountFailoverRetry(ctx, first))

	got, err := s.ListAccountFailoverRetries(ctx)
	mustNoError(t, err)
	if len(got) != 2 || got[0].TurnID != "t1" || got[1].TurnID != "t2" {
		t.Fatalf("list = %+v, want t1 then t2 (oldest first)", got)
	}
	if !got[0].Moved || got[0].Attempts != 2 || !got[0].RetryAt.Equal(t0.Add(time.Hour)) || !got[0].CreatedAt.Equal(t0) {
		t.Fatalf("updated row = %+v", got[0])
	}
	if got[1].Moved || !got[1].RetryAt.IsZero() {
		t.Fatalf("untouched row = %+v", got[1])
	}

	mustNoError(t, s.DeleteAccountFailoverRetry(ctx, "t1"))
	mustNoError(t, s.DeleteAccountFailoverRetry(ctx, "missing"))
	got, err = s.ListAccountFailoverRetries(ctx)
	mustNoError(t, err)
	if len(got) != 1 || got[0].TurnID != "t2" {
		t.Fatalf("after delete = %+v", got)
	}
}
