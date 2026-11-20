package storagejanitor

import (
	"context"
	"errors"
	"testing"
	"time"

	sqlitestore "tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// fakeDetails holds how many rows the activity detail backfill has left.
type fakeDetails struct {
	rows  int64
	calls int
	fail  error
}

func (f *fakeStore) CompactActivityDetailsWindow(_ context.Context, limit int64, _ time.Time) (sqlitestore.ActivityDetailCompaction, error) {
	if f.details == nil || f.details.rows == 0 {
		return sqlitestore.ActivityDetailCompaction{Done: true}, nil
	}
	hold := f.batchCall(limit)
	f.details.calls++
	if f.details.fail != nil {
		return sqlitestore.ActivityDetailCompaction{WriterHold: hold}, f.details.fail
	}
	n := min(limit, f.details.rows)
	f.details.rows -= n
	return sqlitestore.ActivityDetailCompaction{WriterHold: hold, Compacted: n, Done: f.details.rows == 0}, nil
}

// The backfill resumes across passes within each pass's budget and, once
// done, is not asked again.
func TestActivityDetailBackfillResumesAcrossPassesAndFinishes(t *testing.T) {
	store := &fakeStore{details: &fakeDetails{rows: 35}}
	cfg := testConfig(time.Now())
	// One batch finds the tombstone sweep already done, two compact details.
	cfg.MaxBatchesPerPass = 3
	j := New(store, enabled(0), cfg)

	stats, err := j.RunOnce(context.Background())
	if err != nil || stats.ActivityDetailsCompacted != 20 || !stats.OutOfBudget {
		t.Fatalf("first pass = %+v err=%v, want 2 windows of 10 and the budget spent", stats, err)
	}
	stats, err = j.RunOnce(context.Background())
	if err != nil || stats.ActivityDetailsCompacted != 15 || store.details.rows != 0 {
		t.Fatalf("second pass = %+v err=%v, want the remaining 15", stats, err)
	}
	calls := store.details.calls
	if stats, _ = j.RunOnce(context.Background()); stats.ActivityDetailsCompacted != 0 || store.details.calls != calls || stats.Batches != 0 {
		t.Fatalf("finished backfill ran again: %+v calls=%d", stats, store.details.calls)
	}
}

// A failing window never stops the incremental vacuum after it, and is not
// retried until CheckEvery has passed.
func TestFailingActivityDetailBackfillStillVacuumsAndBacksOff(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{details: &fakeDetails{rows: 35, fail: errors.New("database is locked")}, freePages: 150}
	cfg := testConfig(now)
	cfg.Clock = func() time.Time { return now }
	j := New(store, enabled(0), cfg)

	stats, err := j.RunOnce(context.Background())
	if err != nil || !stats.Optimized || store.details.calls != 1 {
		t.Fatalf("stats=%+v calls=%d err=%v, want one failed window and the vacuum still run", stats, store.details.calls, err)
	}
	if _, _ = j.RunOnce(context.Background()); store.details.calls != 1 {
		t.Fatalf("retried before CheckEvery: calls=%d", store.details.calls)
	}
	now = now.Add(DefaultRepackCheckEvery)
	store.details.fail = nil
	if stats, _ = j.RunOnce(context.Background()); store.details.calls < 2 || stats.ActivityDetailsCompacted != 35 {
		t.Fatalf("not retried after CheckEvery: %+v calls=%d", stats, store.details.calls)
	}
}
