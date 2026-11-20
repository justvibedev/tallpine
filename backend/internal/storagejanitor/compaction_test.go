package storagejanitor

import (
	"context"
	"testing"
	"time"

	sqlitestore "tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

func (f *fakeStore) ProviderEventHoldMark(_ context.Context, id string) (sqlitestore.ProviderEventHoldMark, error) {
	f.markReads++
	return f.marks[id], nil
}

// A conversation whose pass ended holding a delta, for example a persistent
// host's settled turn waiting for the next turn.started, costs no compaction
// batch on a later pass while nothing it waits for changed, and is scanned again once
// its hold mark moves.
func TestIdleHeldConversationCostsNoBatchesUntilItsHoldMarkChanges(t *testing.T) {
	store := &fakeStore{
		held:  map[string]bool{"a": true},
		marks: map[string]sqlitestore.ProviderEventHoldMark{"a": {LatestEventID: 40, Turns: 1}},
	}
	j := New(store, enabled(0), testConfig(time.Now()))

	stats, err := j.RunOnce(context.Background())
	if err != nil || len(store.compactCalls) != 1 || stats.HoldsSkipped != 0 {
		t.Fatalf("first pass = %+v err=%v, want one window that holds", stats, err)
	}
	for range 3 {
		stats, err = j.RunOnce(context.Background())
		if err != nil || len(store.compactCalls) != 1 || stats.HoldsSkipped != 1 {
			t.Fatalf("idle pass = %+v err=%v, want no batch for the held conversation", stats, err)
		}
	}
	for name, mark := range map[string]sqlitestore.ProviderEventHoldMark{
		"a later event":          {LatestEventID: 41, Turns: 1},
		"a turn settles":         {LatestEventID: 41, Turns: 1, OpenTurns: 1},
		"the session terminates": {LatestEventID: 41, Turns: 1, OpenTurns: 1, TerminatedSessions: 1},
	} {
		calls := len(store.compactCalls)
		store.marks["a"] = mark
		if stats, err = j.RunOnce(context.Background()); err != nil || len(store.compactCalls) != calls+1 || stats.HoldsSkipped != 0 {
			t.Fatalf("%s: pass = %+v err=%v, want the conversation scanned again", name, stats, err)
		}
		if stats, _ = j.RunOnce(context.Background()); stats.HoldsSkipped != 1 || len(store.compactCalls) != calls+1 {
			t.Fatalf("%s: scanned %d times, want once and then skipped again", name, len(store.compactCalls)-calls)
		}
	}
}
