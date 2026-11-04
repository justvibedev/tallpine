package storagejanitor

import (
	"context"
	"fmt"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	sqlitestore "tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

type fakeStore struct {
	windows      map[string][]sqlitestore.ProviderEventCompaction
	compactCalls []string
	afterIDs     []int64
	watermarks   []int64
	expired      []sqlitestore.ExpiredSession
	sessionRows  map[domain.SessionID]int64
	expireAfter  []int64
	retentionAt  time.Time
	optimizes    int
	freePages    int64
	// clock, when set, advances by holdPerRow for every row a batch is asked
	// for, so tests control how long each batch holds the writer.
	clock      *time.Time
	holdPerRow time.Duration
	limits     []int64
	// contendOn makes the batches with these call numbers (from 1) see a
	// foreground write wait for the lock.
	contendOn map[int]bool
}

func (f *fakeStore) batchCall(limit int64) sqlitestore.WriterHold {
	f.limits = append(f.limits, limit)
	hold := sqlitestore.WriterHold{Held: time.Duration(limit) * f.holdPerRow, Contended: f.contendOn[len(f.limits)]}
	if f.clock != nil {
		*f.clock = f.clock.Add(hold.Held)
	}
	return hold
}

func (f *fakeStore) ProviderEventCompactionCandidates(context.Context) ([]sqlitestore.ConversationCompactionCandidate, error) {
	var out []sqlitestore.ConversationCompactionCandidate
	for _, id := range []string{"a", "b"} {
		if len(f.windows[id]) > 0 {
			out = append(out, sqlitestore.ConversationCompactionCandidate{ConversationID: id, CompactedThroughID: 10})
		}
	}
	return out, nil
}

func (f *fakeStore) CompactProviderEvents(_ context.Context, id string, watermark, after, limit int64, _ time.Time) (sqlitestore.ProviderEventCompaction, error) {
	hold := f.batchCall(limit)
	f.compactCalls = append(f.compactCalls, id)
	f.afterIDs = append(f.afterIDs, after)
	f.watermarks = append(f.watermarks, watermark)
	next := f.windows[id][0]
	f.windows[id] = f.windows[id][1:]
	next.WriterHold = hold
	return next, nil
}

func (f *fakeStore) ExpiredSessionsWithProviderEvents(_ context.Context, before time.Time, _ int64) ([]sqlitestore.ExpiredSession, error) {
	f.retentionAt = before
	return f.expired, nil
}

// ExpireSessionProviderEvents expires rows one id at a time: odd ids carry no
// identity and are deleted, even ones are tombstoned.
func (f *fakeStore) ExpireSessionProviderEvents(_ context.Context, id domain.SessionID, after, limit int64, _ time.Time) (sqlitestore.ProviderEventExpiry, error) {
	hold := f.batchCall(limit)
	f.expireAfter = append(f.expireAfter, after)
	n := min(limit, f.sessionRows[id])
	f.sessionRows[id] -= n
	result := sqlitestore.ProviderEventExpiry{WriterHold: hold, Next: after + n, Done: n < limit}
	for row := after + 1; row <= after+n; row++ {
		if row%2 == 1 {
			result.Deleted++
		} else {
			result.Tombstoned++
		}
	}
	return result, nil
}

func (f *fakeStore) OptimizeStorage(_ context.Context, pages int64) (sqlitestore.StorageMaintenance, error) {
	f.optimizes++
	freed := min(pages, f.freePages)
	f.freePages -= freed
	return sqlitestore.StorageMaintenance{IncrementalVacuum: true, FreedPages: freed}, nil
}

func enabled(retention time.Duration) Policy {
	return func(context.Context) (bool, time.Duration) { return true, retention }
}

func testConfig(now time.Time) Config {
	return Config{Batch: 10, Pause: -1, VacuumPages: 100, Clock: func() time.Time { return now }}
}

func TestDisabledPolicySkipsEverything(t *testing.T) {
	store := &fakeStore{windows: map[string][]sqlitestore.ProviderEventCompaction{"a": {{Done: true}}}}
	j := New(store, func(context.Context) (bool, time.Duration) { return false, time.Hour }, testConfig(time.Now()))
	stats, err := j.RunOnce(context.Background())
	if err != nil || !stats.Skipped || len(store.compactCalls) != 0 || store.optimizes != 0 {
		t.Fatalf("disabled pass did work: stats=%+v calls=%v optimizes=%d err=%v", stats, store.compactCalls, store.optimizes, err)
	}
}

func TestPassCompactsFollowsWatermarksExpiresAndVacuums(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{
		windows: map[string][]sqlitestore.ProviderEventCompaction{
			"a": {{Deleted: 3, Watermark: 15, Next: 20}, {Tombstoned: 2, Watermark: 15, Next: 25, Done: true}},
			"b": {{Deleted: 1, Watermark: 11, Next: 11, Done: true}},
		},
		expired:     []sqlitestore.ExpiredSession{{SessionID: "old-1"}, {SessionID: "old-2", ExpiredThroughID: 100}},
		sessionRows: map[domain.SessionID]int64{"old-1": 25, "old-2": 4},
		freePages:   250,
	}
	j := New(store, enabled(14*24*time.Hour), testConfig(now))
	stats, err := j.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// The second window starts at the first one's cursor, past its held row
	// (watermark 15), so a hold never stops later rows from being compacted.
	if got := store.afterIDs; len(got) != 3 || got[0] != 10 || got[1] != 20 || got[2] != 10 {
		t.Fatalf("compaction windows started after %v, want [10 20 10]", got)
	}
	if got := store.watermarks; got[1] != 15 {
		t.Fatalf("second window got watermark %d, want the held 15 carried over", got[1])
	}
	if stats.Deleted != 4 || stats.Tombstoned != 2 {
		t.Fatalf("stats = %+v", stats)
	}
	if !store.retentionAt.Equal(now.Add(-14 * 24 * time.Hour)) {
		t.Fatalf("retention cutoff = %v", store.retentionAt)
	}
	if stats.ExpiredDeleted+stats.ExpiredTombstoned != 29 || stats.ExpiredTombstoned == 0 ||
		store.sessionRows["old-1"] != 0 || store.sessionRows["old-2"] != 0 {
		t.Fatalf("expired %d deleted + %d tombstoned, left %v", stats.ExpiredDeleted, stats.ExpiredTombstoned, store.sessionRows)
	}
	// Each session resumes at its own cursor, and later windows follow Next.
	if got := store.expireAfter; len(got) != 4 || got[0] != 0 || got[1] != 10 || got[2] != 20 || got[3] != 100 {
		t.Fatalf("expiry windows started after %v, want [0 10 20 100]", got)
	}
	if stats.FreedPages != 250 || store.optimizes != 3 {
		t.Fatalf("vacuum freed %d pages in %d steps, want 250 in 3", stats.FreedPages, store.optimizes)
	}

	// The next pass within OptimizeEvery does not optimize again.
	if _, err := j.RunOnce(context.Background()); err != nil || store.optimizes != 3 {
		t.Fatalf("optimized again too soon: %d err=%v", store.optimizes, err)
	}
}

func TestPassStopsAtItsBatchBudget(t *testing.T) {
	windows := make([]sqlitestore.ProviderEventCompaction, 0, 10)
	for i := range 10 {
		windows = append(windows, sqlitestore.ProviderEventCompaction{Deleted: 1, Watermark: int64(11 + i), Next: int64(11 + i)})
	}
	store := &fakeStore{
		windows:     map[string][]sqlitestore.ProviderEventCompaction{"a": windows},
		expired:     []sqlitestore.ExpiredSession{{SessionID: "old"}},
		sessionRows: map[domain.SessionID]int64{"old": 100},
	}
	cfg := testConfig(time.Now())
	cfg.MaxBatchesPerPass = 4
	stats, err := New(store, enabled(time.Hour), cfg).RunOnce(context.Background())
	if err != nil || stats.Batches != 4 || len(store.compactCalls) != 4 || store.sessionRows["old"] != 100 {
		t.Fatalf("budget not respected: stats=%+v calls=%d rows=%v err=%v", stats, len(store.compactCalls), store.sessionRows, err)
	}
}

// pacedJanitor wires a janitor to the fake's clock and records its pauses.
func pacedJanitor(store *fakeStore, cfg Config) (*Janitor, *[]time.Duration) {
	now := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	store.clock = &now
	j := New(store, enabled(time.Hour), cfg)
	j.now = func() time.Time { return *store.clock }
	var pauses []time.Duration
	j.sleepFn = func(_ context.Context, d time.Duration) bool {
		pauses = append(pauses, d)
		*store.clock = store.clock.Add(d)
		return true
	}
	return j, &pauses
}

func endlessExpiry(rows int64) *fakeStore {
	return &fakeStore{
		expired:     []sqlitestore.ExpiredSession{{SessionID: "old"}},
		sessionRows: map[domain.SessionID]int64{"old": rows},
	}
}

// A batch that holds the writer longer than TargetHold shrinks the next one;
// a quick one grows it back, never past Batch or below MinBatch. Every pause is
// at least HoldRatio times the batch before it.
func TestBatchesAdaptToHoldTheWriterBriefly(t *testing.T) {
	store := endlessExpiry(1 << 40)
	store.holdPerRow = 300 * time.Microsecond // 100 rows hold for 30ms, 50 for 15ms
	cfg := Config{Batch: 400, MinBatch: 25, TargetHold: 10 * time.Millisecond, HoldRatio: 3, Pause: time.Millisecond, MaxBatchesPerPass: 8}
	j, pauses := pacedJanitor(store, cfg)
	stats, err := j.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.BatchSize != 50 {
		t.Fatalf("reported batch size %d, want the adapted 50", stats.BatchSize)
	}
	want := []int64{100, 50, 50, 50, 50, 50, 50, 50}
	if fmt.Sprint(store.limits) != fmt.Sprint(want) {
		t.Fatalf("batch sizes = %v, want %v", store.limits, want)
	}
	for i, limit := range store.limits {
		held := time.Duration(limit) * store.holdPerRow
		if (*pauses)[i] < 3*held {
			t.Fatalf("pause %d = %v after holding %v, want at least %v", i, (*pauses)[i], held, 3*held)
		}
	}

	fast := endlessExpiry(1 << 40)
	fast.holdPerRow = time.Microsecond
	j, _ = pacedJanitor(fast, cfg)
	if _, err := j.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := fast.limits[len(fast.limits)-1]; got != cfg.Batch {
		t.Fatalf("quick batches grew to %d, want the %d cap (sizes %v)", got, cfg.Batch, fast.limits)
	}
}

// When a foreground write waits for the writer lock during a batch, the next
// pause grows, doubling while the contention lasts and decaying after it.
func TestContentionBacksOffAndDecays(t *testing.T) {
	store := endlessExpiry(1 << 40)
	store.holdPerRow = time.Microsecond
	store.contendOn = map[int]bool{2: true, 3: true, 4: true}
	cfg := Config{Batch: 10, MinBatch: 10, TargetHold: time.Second, HoldRatio: 3, Pause: 10 * time.Millisecond, MaxBackoff: 25 * time.Millisecond, MaxBatchesPerPass: 7}
	j, pauses := pacedJanitor(store, cfg)
	stats, err := j.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ms := time.Millisecond
	want := []time.Duration{10 * ms, 20 * ms, 30 * ms, 35 * ms, 22500 * time.Microsecond, 16250 * time.Microsecond, 13125 * time.Microsecond}
	if fmt.Sprint(*pauses) != fmt.Sprint(want) {
		t.Fatalf("pauses = %v, want %v", *pauses, want)
	}
	if stats.Backoffs != 3 {
		t.Fatalf("backoffs = %d, want 3", stats.Backoffs)
	}
}

// A pass stops at MaxPassTime, pauses included, and the next pass resumes.
func TestPassStopsAtItsTimeBudget(t *testing.T) {
	store := endlessExpiry(1 << 40)
	store.holdPerRow = time.Millisecond
	cfg := Config{Batch: 10, MinBatch: 10, TargetHold: time.Second, HoldRatio: 3, Pause: time.Millisecond, MaxPassTime: 200 * time.Millisecond}
	j, _ := pacedJanitor(store, cfg)
	stats, err := j.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Each batch holds 10ms and pauses 30ms: five fit in 200ms.
	if stats.Batches != 5 {
		t.Fatalf("batches = %d, want 5 within the 200ms budget", stats.Batches)
	}
}

// convertingStore adds the optional auto-vacuum conversion surface.
type convertingStore struct {
	*fakeStore
	writes      uint64
	incremental bool
	attempts    int
	outcome     sqlitestore.AutoVacuumConversion
	opts        sqlitestore.AutoVacuumConversionOptions
}

func (c *convertingStore) WriterAcquisitions() uint64 { return c.writes }
func (c *convertingStore) IncrementalAutoVacuum(context.Context) (bool, error) {
	return c.incremental, nil
}
func (c *convertingStore) ConvertToIncrementalAutoVacuum(_ context.Context, opts sqlitestore.AutoVacuumConversionOptions) (sqlitestore.AutoVacuumConversion, error) {
	c.attempts++
	c.opts = opts
	if c.outcome.Converted {
		c.incremental = true
	}
	return c.outcome, nil
}

// The one-time conversion waits for a drained backlog and QuietPasses quiet
// intervals, spaces retries after an interrupted attempt, and stops for good
// once the database is converted.
func TestAutoVacuumConversionWaitsForAQuietDrainedDatabase(t *testing.T) {
	now := time.Date(2026, 10, 26, 3, 0, 0, 0, time.UTC)
	store := &convertingStore{fakeStore: &fakeStore{}, outcome: sqlitestore.AutoVacuumConversion{Interrupted: true}}
	cfg := testConfig(now)
	cfg.Clock = func() time.Time { return now }
	cfg.QuietWrites, cfg.QuietPasses, cfg.ConversionRetry = 5, 2, time.Hour
	cfg.ConversionWriteGrace = 250 * time.Millisecond
	j := New(store, enabled(0), cfg)
	pass := func(writesBefore uint64) Stats {
		t.Helper()
		store.writes += writesBefore
		stats, err := j.RunOnce(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return stats
	}

	pass(0)  // first pass: the interval before it is unmeasured
	pass(50) // busy interval
	pass(1)  // quiet 1
	if store.attempts != 0 {
		t.Fatalf("converted after one quiet interval")
	}
	if s := pass(2); s.Conversion == nil || !s.Conversion.Interrupted || store.attempts != 1 {
		t.Fatalf("no attempt after two quiet intervals: attempts=%d stats=%+v", store.attempts, s)
	}
	if store.opts.WriteGrace != 250*time.Millisecond || store.opts.MaxLiveBytes != DefaultConversionMaxLive {
		t.Fatalf("conversion options = %+v, want the configured grace and the default live bound", store.opts)
	}
	pass(0)
	pass(0)
	if store.attempts != 1 {
		t.Fatalf("retried %d times inside ConversionRetry", store.attempts-1)
	}

	now = now.Add(2 * time.Hour)
	store.outcome = sqlitestore.AutoVacuumConversion{Converted: true}
	// A pass that runs out of budget with work left is not drained.
	store.windows = map[string][]sqlitestore.ProviderEventCompaction{"a": {{Deleted: 7}, {Deleted: 7}, {Deleted: 7, Done: true}}}
	j.cfg.MaxBatchesPerPass = 2
	if s := pass(0); !s.OutOfBudget {
		t.Fatalf("pass with work left did not report OutOfBudget: %+v", s)
	}
	j.cfg.MaxBatchesPerPass = 100
	pass(0)
	if store.attempts != 1 {
		t.Fatalf("converted with a backlog left")
	}
	pass(0)
	if store.attempts != 2 || !store.incremental {
		t.Fatalf("attempts=%d incremental=%v, want a second, successful attempt", store.attempts, store.incremental)
	}
	now = now.Add(24 * time.Hour)
	for range 5 {
		pass(0)
	}
	if store.attempts != 2 {
		t.Fatalf("converted again after success: %d attempts", store.attempts)
	}
}
