package storagejanitor

import (
	"context"
	"reflect"
	"testing"
	"time"

	sqlitestore "tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// fakeHistory prunes rows one id/sequence at a time below a per-conversation
// settled boundary.
type fakeHistory struct {
	candidates    []sqlitestore.HistoryRetentionCandidate
	settled       map[string]int64
	eventRows     map[string]int64
	activityRows  map[string]int64
	eventCalls    []string
	activityCalls []string
	boundaries    []int64
}

func (f *fakeStore) HistoryRetentionCandidates(context.Context) ([]sqlitestore.HistoryRetentionCandidate, error) {
	if f.history == nil {
		return nil, nil
	}
	return f.history.candidates, nil
}

func (f *fakeStore) SettledHistoryBoundary(_ context.Context, id string, after int64) (int64, time.Time, error) {
	if b := f.history.settled[id]; b > after {
		return b, time.Unix(b, 0), nil
	}
	return after, time.Unix(after, 0), nil
}

func (f *fakeStore) PruneHistoryEvents(_ context.Context, id string, boundary, after, limit int64, _ time.Time) (sqlitestore.HistoryEventPrune, error) {
	hold := f.batchCall(limit)
	f.history.eventCalls = append(f.history.eventCalls, id)
	f.history.boundaries = append(f.history.boundaries, boundary)
	n := min(limit, f.history.eventRows[id])
	f.history.eventRows[id] -= n
	result := sqlitestore.HistoryEventPrune{WriterHold: hold, Deleted: n, Next: after + n, Done: n < limit}
	if result.Done {
		result.Next = boundary
	}
	return result, nil
}

func (f *fakeStore) PruneHistoryActivities(_ context.Context, id string, _ int64, _ time.Time, _, after, limit int64, _ time.Time) (sqlitestore.HistoryActivityPrune, error) {
	hold := f.batchCall(limit)
	f.history.activityCalls = append(f.history.activityCalls, id)
	n := min(limit, f.history.activityRows[id])
	f.history.activityRows[id] -= n
	return sqlitestore.HistoryActivityPrune{WriterHold: hold, Pruned: n, Watermark: after + n, Next: after + n, Done: n < limit}, nil
}

// Break caught: compaction only shrank the model's context while the archive
// janitor never touched a live conversation, so a long-lived orchestrator kept
// every provider event and command output forever. A pass now prunes each
// compacted conversation below its settled boundary, resumes from the stored
// cursors, and leaves a conversation whose compaction has not settled alone.
func TestPassPrunesHistoryBelowSettledCompaction(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	store := &fakeStore{history: &fakeHistory{
		candidates: []sqlitestore.HistoryRetentionCandidate{
			{ConversationID: "orch", LatestCompactionID: 100},
			// Its events are done for boundary 50; only activities remain.
			{ConversationID: "done-events", LatestCompactionID: 50, BoundaryEventID: 50, EventsPrunedThroughID: 50},
			// The newest compaction has no later turn yet: nothing settles.
			{ConversationID: "unsettled", LatestCompactionID: 70},
		},
		settled:      map[string]int64{"orch": 100, "done-events": 50},
		eventRows:    map[string]int64{"orch": 25, "done-events": 99},
		activityRows: map[string]int64{"orch": 15, "done-events": 5},
	}}
	stats, err := New(store, enabled(0), testConfig(now)).RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	h := store.history
	if want := []string{"orch", "orch", "orch"}; !reflect.DeepEqual(h.eventCalls, want) {
		t.Fatalf("event prune calls = %v, want %v", h.eventCalls, want)
	}
	if want := []string{"orch", "orch", "done-events"}; !reflect.DeepEqual(h.activityCalls, want) {
		t.Fatalf("activity prune calls = %v, want %v", h.activityCalls, want)
	}
	if h.boundaries[0] != 100 {
		t.Fatalf("boundary = %d, want the settled compaction 100", h.boundaries[0])
	}
	if stats.HistoryDeleted != 25 || stats.HistoryActivities != 20 {
		t.Fatalf("stats = %+v, want 25 events and 20 activities pruned", stats)
	}
}
