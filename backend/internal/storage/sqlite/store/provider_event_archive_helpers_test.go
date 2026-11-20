package store_test

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

// Helpers shared by the provider-event retention, identity-key and repack
// tests: expire every session past a cutoff, and mark a session terminated.

func expireAll(t *testing.T, s *sqlite.Store, before time.Time, batch int64) (deleted, keyed int64) {
	t.Helper()
	ctx := context.Background()
	expired, err := s.ExpiredSessionsWithProviderEvents(ctx, before, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range expired {
		after := session.ExpiredThroughID
		for range 1000 {
			result, err := s.ExpireSessionProviderEvents(ctx, session.SessionID, after, batch, histClock)
			if err != nil {
				t.Fatal(err)
			}
			if result.Deleted+result.Keyed > batch {
				t.Fatalf("one window expired %d rows, want at most %d", result.Deleted+result.Keyed, batch)
			}
			deleted += result.Deleted
			keyed += result.Keyed
			after = result.Next
			if result.Done {
				break
			}
		}
	}
	return deleted, keyed
}

func terminate(t *testing.T, s *sqlite.Store, session domain.SessionID, at time.Time) {
	t.Helper()
	rec, _, err := s.GetSession(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	rec.IsTerminated = true
	rec.UpdatedAt = at
	if err := s.UpdateSession(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
}
