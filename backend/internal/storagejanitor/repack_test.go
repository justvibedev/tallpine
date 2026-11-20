package storagejanitor

import (
	"context"
	"errors"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	sqlitestore "tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// fakeRepack reports a fixed slack and rewrites one id per row asked for.
type fakeRepack struct {
	// tombstones is how many legacy tombstones the sweep still has to delete.
	tombstones int64
	sweeps     int
	slack      sqlitestore.ProviderEventArchiveSlack
	measures   int
	calls      [][2]int64
	rewrote    int64
	fail       error
	sweepFail  error
}

func (f *fakeStore) SweepProviderEventTombstones(_ context.Context, limit int64, _ time.Time) (sqlitestore.TombstoneSweep, error) {
	if f.repack == nil || f.repack.tombstones == 0 {
		return sqlitestore.TombstoneSweep{Done: true}, nil
	}
	hold := f.batchCall(limit)
	f.repack.sweeps++
	if f.repack.sweepFail != nil {
		return sqlitestore.TombstoneSweep{WriterHold: hold}, f.repack.sweepFail
	}
	n := min(limit, f.repack.tombstones)
	f.repack.tombstones -= n
	return sqlitestore.TombstoneSweep{WriterHold: hold, Deleted: n, Done: f.repack.tombstones == 0}, nil
}

func (f *fakeStore) ProviderEventArchiveSlack(context.Context) (sqlitestore.ProviderEventArchiveSlack, error) {
	if f.repack == nil {
		return sqlitestore.ProviderEventArchiveSlack{}, nil
	}
	f.repack.measures++
	return f.repack.slack, nil
}

func (f *fakeStore) RepackProviderEvents(_ context.Context, after, through, limit int64) (sqlitestore.ProviderEventRepack, error) {
	hold := f.batchCall(limit)
	f.repack.calls = append(f.repack.calls, [2]int64{after, through})
	if f.repack.fail != nil {
		return sqlitestore.ProviderEventRepack{WriterHold: hold, Next: after}, f.repack.fail
	}
	next := min(after+limit, through)
	f.repack.rewrote += next - after
	return sqlitestore.ProviderEventRepack{WriterHold: hold, Rewritten: next - after, Next: next, Done: next == through}, nil
}

func slackStore(bytes, unused, maxID int64) *fakeStore {
	return &fakeStore{repack: &fakeRepack{slack: sqlitestore.ProviderEventArchiveSlack{Bytes: bytes, UnusedBytes: unused, MaxID: maxID}}}
}

// A drained pass measures the archive once a day; enough slack starts a sweep
// that resumes across passes until it reaches the id measured.
func TestRepackSweepsTheArchiveOnceItHasEnoughSlack(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	store := slackStore(1000<<20, 400<<20, 35)
	cfg := testConfig(now)
	cfg.Clock = func() time.Time { return now }
	// One batch finds the tombstone sweep already done, two repack.
	cfg.MaxBatchesPerPass = 3
	j := New(store, enabled(0), cfg)

	stats, err := j.RunOnce(context.Background())
	if err != nil || !stats.RepackMeasured || !stats.RepackStarted || stats.Repacked != 20 {
		t.Fatalf("first pass = %+v err=%v, want a sweep of 2 batches of 10", stats, err)
	}
	stats, err = j.RunOnce(context.Background())
	if err != nil || stats.RepackMeasured || stats.Repacked != 15 {
		t.Fatalf("second pass = %+v err=%v, want the sweep resumed without measuring", stats, err)
	}
	want := [][2]int64{{0, 35}, {10, 35}, {20, 35}, {30, 35}}
	if got := store.repack.calls; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] || got[3] != want[3] {
		t.Fatalf("repack windows = %v, want %v", got, want)
	}

	// Finished: no measurement again within the day, one after it.
	if stats, _ = j.RunOnce(context.Background()); stats.RepackMeasured || store.repack.measures != 1 {
		t.Fatalf("measured again within the day: %+v", stats)
	}
	now = now.Add(DefaultRepackCheckEvery)
	if stats, _ = j.RunOnce(context.Background()); !stats.RepackMeasured || store.repack.measures != 2 {
		t.Fatalf("did not measure after a day: %+v", stats)
	}
}

func TestRepackNeedsSlackInBytesAndInShare(t *testing.T) {
	for name, store := range map[string]*fakeStore{
		"too few bytes":   slackStore(200<<20, 100<<20, 50),
		"too small share": slackStore(4000<<20, 900<<20, 50),
	} {
		stats, err := New(store, enabled(0), testConfig(time.Now())).RunOnce(context.Background())
		if err != nil || !stats.RepackMeasured || stats.RepackStarted || len(store.repack.calls) != 0 {
			t.Fatalf("%s: stats=%+v calls=%v err=%v, want measured and left alone", name, stats, store.repack.calls, err)
		}
	}
}

// A pass that ran out of budget still has rows to remove; repacking them
// first would only rewrite them twice.
func TestRepackWaitsForTheArchiveBacklog(t *testing.T) {
	store := slackStore(1000<<20, 400<<20, 50)
	store.expired = []sqlitestore.ExpiredSession{{SessionID: "old"}}
	store.sessionRows = map[domain.SessionID]int64{"old": 100}
	cfg := testConfig(time.Now())
	cfg.MaxBatchesPerPass = 3
	stats, err := New(store, enabled(time.Hour), cfg).RunOnce(context.Background())
	if err != nil || !stats.OutOfBudget || stats.RepackMeasured || store.repack.measures != 0 {
		t.Fatalf("stats=%+v err=%v, want no measurement while the backlog is undrained", stats, err)
	}
}

// A window that fails every time ends the sweep instead of stopping every
// later pass before incremental vacuum.
func TestFailingRepackStillLetsThePassVacuum(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	store := slackStore(1000<<20, 400<<20, 35)
	store.repack.fail = errors.New("FOREIGN KEY constraint failed")
	store.freePages = 150
	cfg := testConfig(now)
	j := New(store, enabled(0), cfg)

	stats, err := j.RunOnce(context.Background())
	if err != nil || !stats.RepackFailed || stats.FreedPages != 150 || store.optimizes == 0 {
		t.Fatalf("pass = %+v err=%v optimizes=%d, want the failure logged and the vacuum run", stats, err, store.optimizes)
	}
	calls := len(store.repack.calls)
	if stats, err = j.RunOnce(context.Background()); err != nil || stats.RepackMeasured || len(store.repack.calls) != calls {
		t.Fatalf("next pass = %+v err=%v, %d windows (was %d): want no retry before the next measurement", stats, err, len(store.repack.calls), calls)
	}
}

// The one-time tombstone sweep runs paced across passes before any repack
// measurement, and stops being called once it reports done.
func TestTombstoneSweepRunsOnceBeforeTheRepack(t *testing.T) {
	store := slackStore(1000<<20, 400<<20, 15)
	store.repack.tombstones = 25
	cfg := testConfig(time.Now())
	cfg.MaxBatchesPerPass = 2
	j := New(store, enabled(0), cfg)

	stats, err := j.RunOnce(context.Background())
	if err != nil || stats.TombstonesSwept != 20 || stats.RepackMeasured || store.repack.measures != 0 {
		t.Fatalf("first pass = %+v err=%v, want 2 sweep batches and no repack yet", stats, err)
	}
	stats, err = j.RunOnce(context.Background())
	if err != nil || stats.TombstonesSwept != 5 || !stats.RepackMeasured || stats.Repacked != 10 {
		t.Fatalf("second pass = %+v err=%v, want the sweep finished, then the repack", stats, err)
	}
	sweeps := store.repack.sweeps
	if _, err := j.RunOnce(context.Background()); err != nil || store.repack.sweeps != sweeps {
		t.Fatalf("sweep called again after done: %d calls, want %d", store.repack.sweeps, sweeps)
	}
}

// A failing tombstone sweep does not stop the pass, and is retried after
// CheckEvery rather than on every pass.
func TestFailingTombstoneSweepBacksOff(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	store := slackStore(1000<<20, 400<<20, 15)
	store.repack.tombstones = 5
	store.repack.sweepFail = errors.New("disk I/O error")
	store.freePages = 50
	cfg := testConfig(now)
	cfg.Clock = func() time.Time { return now }
	j := New(store, enabled(0), cfg)

	stats, err := j.RunOnce(context.Background())
	if err != nil || !stats.RepackFailed || stats.FreedPages != 50 || store.repack.sweeps != 1 {
		t.Fatalf("pass = %+v err=%v sweeps=%d, want the failure logged and the vacuum run", stats, err, store.repack.sweeps)
	}
	if _, err := j.RunOnce(context.Background()); err != nil || store.repack.sweeps != 1 {
		t.Fatalf("sweep retried within the day: %d calls err=%v", store.repack.sweeps, err)
	}
	now = now.Add(DefaultRepackCheckEvery)
	store.repack.sweepFail = nil
	if stats, err := j.RunOnce(context.Background()); err != nil || stats.TombstonesSwept != 5 {
		t.Fatalf("retry after a day = %+v err=%v, want the sweep finished", stats, err)
	}
}
