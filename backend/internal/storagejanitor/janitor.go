// Package storagejanitor keeps the SQLite provider-event archive bounded.
//
// It compacts streamed deltas of settled turns, prunes each conversation's
// history below its last settled compaction boundary (see history.go), deletes
// the provider events of sessions terminated longer ago than the retention
// window, repacks the archive's half-empty pages (see repack.go), and runs
// SQLite's cheap maintenance pragmas. Every unit of work is a small transaction followed
// by a pause, so chat projection writes never queue behind it for long.
//
// The pacing adapts to the database and to the daemon's load: a batch is sized
// so it holds the single writer lock for about TargetHold, the pause after it
// is at least HoldRatio times as long as the batch held the lock, and whenever
// another write had to wait for the lock during a batch the janitor backs off
// further, up to MaxBackoff, so it still makes steady progress under sustained
// load. A large first backlog is worked off over several passes in the
// background rather than as a write storm.
//
// A database created before incremental auto-vacuum never shrinks: freed pages
// stay in the file. Once its backlog is drained and the daemon has been quiet
// for a while, the janitor converts it with one VACUUM that gives way to the
// first foreground write (see Store.ConvertToIncrementalAutoVacuum), retrying
// at a later quiet moment if it is interrupted.
package storagejanitor

import (
	"context"
	"log/slog"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	sqlitestore "tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

const (
	// DefaultInterval paces passes. Compaction only needs to keep up with turns
	// settling, and retention with sessions crossing the window.
	DefaultInterval = 2 * time.Minute
	// DefaultStartDelay keeps the first pass away from daemon startup, which is
	// busy reconciling sessions and reattaching controllers.
	DefaultStartDelay = time.Minute
	// DefaultBatch is the most rows one transaction looks at or deletes. The
	// janitor starts below it and adapts toward TargetHold.
	DefaultBatch int64 = 500
	// DefaultMinBatch is the fewest rows a batch shrinks to on a slow disk.
	DefaultMinBatch int64 = 50
	// DefaultTargetHold is how long one batch should hold the writer lock. A
	// foreground write that arrives mid-batch waits at most about this long.
	DefaultTargetHold = 10 * time.Millisecond
	// DefaultHoldRatio makes every pause at least this many times the batch
	// that preceded it, so the janitor holds the writer at most 1/(1+ratio) of
	// the time.
	DefaultHoldRatio = 3
	// DefaultPause separates transactions so waiting writers get the lock.
	DefaultPause = 25 * time.Millisecond
	// DefaultMaxBackoff caps the extra pause added while foreground writes keep
	// queueing behind the janitor. Under constant load the janitor then holds
	// the writer a few percent of the time, short batches at a time, and still
	// drains a large backlog within the hour.
	DefaultMaxBackoff = 250 * time.Millisecond
	// DefaultMaxBatchesPerPass bounds one pass. A large backlog is worked off
	// over several passes rather than in one long burst.
	DefaultMaxBatchesPerPass = 2000
	// DefaultMaxPassTime bounds one pass in wall time, pauses included, so a
	// pass ends well before the next one is due.
	DefaultMaxPassTime = time.Minute
	// DefaultQuietWrites is the most writer-lock acquisitions between two
	// passes for the daemon to count as quiet. Idle observers write a little.
	DefaultQuietWrites = 30
	// DefaultQuietPasses is how many quiet intervals in a row precede a
	// conversion to incremental auto-vacuum.
	DefaultQuietPasses = 3
	// DefaultConversionRetry spaces conversion attempts, so an interrupted or
	// skipped one is not repeated every pass.
	DefaultConversionRetry = time.Hour
	// DefaultConversionMaxLive bounds the live data a conversion rewrites. Even
	// an idle daemon writes every few seconds, and a VACUUM gives way to the
	// first write, so only a database that rebuilds in seconds converts online.
	// It also bounds how long a write can wait: an interrupt is not noticed
	// inside some VACUUM phases (~0.5 s per 200 MB measured), so at 256 MiB a
	// write waits at most about the grace plus 0.7 s.
	DefaultConversionMaxLive int64 = 256 << 20
	// DefaultConversionWriteGrace is how long a write may wait for the
	// conversion's VACUUM before interrupting it.
	DefaultConversionWriteGrace = 500 * time.Millisecond
	// DefaultOptimizeEvery paces PRAGMA optimize and incremental vacuum.
	DefaultOptimizeEvery = time.Hour
	// DefaultVacuumPages is how many free pages one incremental vacuum step
	// returns to the OS (8 MiB at the default 4 KiB page size).
	DefaultVacuumPages int64 = 2048
	// maxVacuumSteps bounds incremental vacuum steps per maintenance run.
	maxVacuumSteps = 64
	// expiredSessionPage is how many expired sessions one pass looks up.
	expiredSessionPage int64 = 50
)

// Store is the storage surface the janitor needs.
type Store interface {
	ProviderEventCompactionCandidates(ctx context.Context) ([]sqlitestore.ConversationCompactionCandidate, error)
	CompactProviderEvents(ctx context.Context, conversationID string, watermark, after, limit int64, now time.Time) (sqlitestore.ProviderEventCompaction, error)
	ProviderEventHoldMark(ctx context.Context, conversationID string) (sqlitestore.ProviderEventHoldMark, error)
	ExpiredSessionsWithProviderEvents(ctx context.Context, before time.Time, limit int64) ([]sqlitestore.ExpiredSession, error)
	ExpireSessionProviderEvents(ctx context.Context, session domain.SessionID, after, limit int64, now time.Time) (sqlitestore.ProviderEventExpiry, error)
	OptimizeStorage(ctx context.Context, maxVacuumPages int64) (sqlitestore.StorageMaintenance, error)
	HistoryStore
	RepackStore
	ActivityDetailStore
}

// autoVacuumConverter is the optional store surface for converting a database
// created before incremental auto-vacuum.
type autoVacuumConverter interface {
	IncrementalAutoVacuum(ctx context.Context) (bool, error)
	ConvertToIncrementalAutoVacuum(ctx context.Context, opts sqlitestore.AutoVacuumConversionOptions) (sqlitestore.AutoVacuumConversion, error)
	WriterAcquisitions() uint64
}

// Policy reports whether the janitor may run and the retention window. It is
// read at the start of every pass, so a settings change applies to the next one.
type Policy func(ctx context.Context) (enabled bool, retention time.Duration)

// Config customizes the janitor. Zero values use the production defaults.
type Config struct {
	Interval          time.Duration
	StartDelay        time.Duration
	Batch             int64
	MinBatch          int64
	TargetHold        time.Duration
	HoldRatio         int
	Pause             time.Duration
	MaxBackoff        time.Duration
	MaxBatchesPerPass int
	MaxPassTime       time.Duration
	QuietWrites       int
	QuietPasses       int
	ConversionRetry   time.Duration
	ConversionMaxLive int64
	// ConversionWriteGrace is how long a write may wait for the conversion's
	// VACUUM before interrupting it.
	ConversionWriteGrace time.Duration
	OptimizeEvery        time.Duration
	VacuumPages          int64
	Repack               RepackConfig
	Clock                func() time.Time
	Logger               *slog.Logger
}

// Stats reports one pass.
type Stats struct {
	Skipped bool
	Batches int
	// OutOfBudget reports that the pass stopped at its batch or time budget
	// with archive work left: the backlog is not drained yet.
	OutOfBudget bool
	// Backoffs counts batches during which a foreground write waited for the
	// writer lock, each of which lengthened the following pause.
	Backoffs int
	// BatchSize is the adapted batch size at the end of the pass.
	BatchSize int64
	Deleted   int64
	Keyed     int64
	// HoldsSkipped counts conversations left alone because nothing their
	// held deltas wait for changed since the pass that held them.
	HoldsSkipped   int
	ExpiredDeleted int64
	// ExpiredKeyed counts expired rows whose identity was kept as a key.
	ExpiredKeyed int64
	HistoryStats
	RepackStats
	ActivityDetailStats
	Optimized  bool
	FreedPages int64
	// ForegroundWrites counts writer acquisitions since the previous pass,
	// or -1 when that interval was not measured.
	ForegroundWrites int64
	// Conversion is the auto-vacuum conversion attempted in this pass, if any.
	Conversion *sqlitestore.AutoVacuumConversion
}

// Janitor compacts and expires the provider-event archive in the background.
type Janitor struct {
	store        Store
	policy       Policy
	cfg          Config
	lastOptimize time.Time
	// batch is the adapted batch size, kept across passes.
	batch int64
	// backoff is the extra pause owed to recent writer contention.
	backoff time.Duration
	// passStart and now time the pass budget; now is the monotonic clock.
	passStart time.Time
	now       func() time.Time
	sleepFn   func(ctx context.Context, d time.Duration) bool
	// Auto-vacuum conversion state: the acquisitions count when the last
	// pass ended, consecutive quiet intervals, and the last attempt.
	converter       autoVacuumConverter
	converted       bool
	lastAcquired    uint64
	measuredWrites  bool
	quietPasses     int
	lastConversion  time.Time
	skipLogged      bool
	repackState     repackState
	activityDetails activityDetailState
	holds           holds
}

// New builds a janitor.
func New(store Store, policy Policy, cfg Config) *Janitor {
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultInterval
	}
	if cfg.StartDelay < 0 {
		cfg.StartDelay = 0
	} else if cfg.StartDelay == 0 {
		cfg.StartDelay = DefaultStartDelay
	}
	if cfg.Batch <= 0 {
		cfg.Batch = DefaultBatch
	}
	if cfg.MinBatch <= 0 {
		cfg.MinBatch = DefaultMinBatch
	}
	cfg.MinBatch = min(cfg.MinBatch, cfg.Batch)
	if cfg.TargetHold <= 0 {
		cfg.TargetHold = DefaultTargetHold
	}
	if cfg.HoldRatio <= 0 {
		cfg.HoldRatio = DefaultHoldRatio
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = DefaultMaxBackoff
	}
	if cfg.MaxPassTime <= 0 {
		cfg.MaxPassTime = DefaultMaxPassTime
	}
	if cfg.QuietWrites <= 0 {
		cfg.QuietWrites = DefaultQuietWrites
	}
	if cfg.QuietPasses <= 0 {
		cfg.QuietPasses = DefaultQuietPasses
	}
	if cfg.ConversionRetry <= 0 {
		cfg.ConversionRetry = DefaultConversionRetry
	}
	if cfg.ConversionMaxLive <= 0 {
		cfg.ConversionMaxLive = DefaultConversionMaxLive
	}
	if cfg.ConversionWriteGrace <= 0 {
		cfg.ConversionWriteGrace = DefaultConversionWriteGrace
	}
	if cfg.Pause < 0 {
		cfg.Pause = 0
	} else if cfg.Pause == 0 {
		cfg.Pause = DefaultPause
	}
	if cfg.MaxBatchesPerPass <= 0 {
		cfg.MaxBatchesPerPass = DefaultMaxBatchesPerPass
	}
	if cfg.OptimizeEvery <= 0 {
		cfg.OptimizeEvery = DefaultOptimizeEvery
	}
	if cfg.VacuumPages <= 0 {
		cfg.VacuumPages = DefaultVacuumPages
	}
	cfg.Repack = cfg.Repack.withDefaults()
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	j := &Janitor{store: store, policy: policy, cfg: cfg, now: time.Now}
	// Start small: the first batches of a cold backlog are the slowest.
	j.batch = min(max(cfg.Batch/4, cfg.MinBatch), cfg.Batch)
	j.sleepFn = j.sleep
	j.converter, _ = store.(autoVacuumConverter)
	return j
}

// Start runs passes after StartDelay and then every Interval until ctx ends.
// The returned channel closes once the goroutine has exited, so the caller can
// wait for it before closing the store.
func (j *Janitor) Start(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		if !j.sleep(ctx, j.cfg.StartDelay) {
			return
		}
		ticker := time.NewTicker(j.cfg.Interval)
		defer ticker.Stop()
		for {
			j.run(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return done
}

func (j *Janitor) run(ctx context.Context) {
	stats, err := j.RunOnce(ctx)
	if err != nil && ctx.Err() == nil {
		j.cfg.Logger.Warn("provider event archive maintenance failed", "err", err)
		return
	}
	if stats.Deleted+stats.Keyed+stats.ExpiredDeleted+stats.ExpiredKeyed > 0 ||
		stats.HistoryDeleted+stats.HistoryKeyed+stats.HistoryActivities+stats.Repacked+stats.TombstonesSwept+stats.ActivityDetailsCompacted > 0 || stats.FreedPages > 0 {
		j.cfg.Logger.Info("provider event archive maintenance",
			"deltasDeleted", stats.Deleted, "deltasKeyed", stats.Keyed,
			"expiredDeleted", stats.ExpiredDeleted, "expiredKeyed", stats.ExpiredKeyed,
			"historyDeleted", stats.HistoryDeleted, "historyKeyed", stats.HistoryKeyed,
			"historyActivities", stats.HistoryActivities, "repacked", stats.Repacked, "tombstonesSwept", stats.TombstonesSwept,
			"activityDetailsCompacted", stats.ActivityDetailsCompacted,
			"freedPages", stats.FreedPages, "batches", stats.Batches, "batchSize", stats.BatchSize,
			"backoffs", stats.Backoffs, "foregroundWrites", stats.ForegroundWrites,
			"elapsed", j.now().Sub(j.passStart).Round(time.Millisecond))
	}
}

// RunOnce performs one bounded pass: compaction, then history pruning, then
// retention, then the archive repack (see repack.go), then the activity detail
// backfill (see activity_details.go), then (at most every
// OptimizeEvery) PRAGMA optimize and incremental vacuum.
func (j *Janitor) RunOnce(ctx context.Context) (Stats, error) {
	var stats Stats
	enabled, retention := j.policy(ctx)
	if !enabled {
		stats.Skipped = true
		return stats, nil
	}
	j.passStart = j.now()
	stats.ForegroundWrites = j.foregroundWrites()
	stats, err := j.pass(ctx, stats, retention)
	stats.BatchSize = j.batch
	return stats, err
}

func (j *Janitor) pass(ctx context.Context, stats Stats, retention time.Duration) (Stats, error) {
	if err := j.compact(ctx, &stats); err != nil {
		return stats, err
	}
	if err := j.pruneHistory(ctx, &stats); err != nil {
		return stats, err
	}
	if retention > 0 {
		if err := j.expire(ctx, retention, &stats); err != nil {
			return stats, err
		}
	}
	if err := j.repack(ctx, &stats); err != nil {
		return stats, err
	}
	j.compactActivityDetails(ctx, &stats)
	if now := j.cfg.Clock(); j.lastOptimize.IsZero() || now.Sub(j.lastOptimize) >= j.cfg.OptimizeEvery {
		if err := j.optimize(ctx, &stats); err != nil {
			return stats, err
		}
		j.lastOptimize = now
	}
	if err := j.maybeConvert(ctx, &stats); err != nil {
		return stats, err
	}
	j.markPassEnd()
	return stats, nil
}

// foregroundWrites counts writer acquisitions since the previous pass ended,
// when the janitor itself was not writing, or returns -1 if that is unknown.
func (j *Janitor) foregroundWrites() int64 {
	if j.converter == nil || !j.measuredWrites {
		return -1
	}
	return int64(j.converter.WriterAcquisitions() - j.lastAcquired) // #nosec G115 -- acquisitions in one interval are far below 2^63
}

func (j *Janitor) markPassEnd() {
	if j.converter != nil {
		j.lastAcquired, j.measuredWrites = j.converter.WriterAcquisitions(), true
	}
}

func (j *Janitor) budgetLeft(stats *Stats) bool {
	if stats.Batches < j.cfg.MaxBatchesPerPass && j.now().Sub(j.passStart) < j.cfg.MaxPassTime {
		return true
	}
	stats.OutOfBudget = true
	return false
}

// paced runs one batch and then pauses for as long as the batch earned: at
// least HoldRatio times as long as it held the writer, more while foreground
// writes keep queueing behind it. It also adapts the next batch size toward
// TargetHold. The store measures the hold itself, so time spent waiting for
// the lock behind busy writers is not mistaken for a slow batch.
func (j *Janitor) paced(ctx context.Context, stats *Stats, batch func(limit int64) (sqlitestore.WriterHold, error)) error {
	hold, err := batch(j.batch)
	if err != nil {
		return err
	}
	held := hold.Held
	stats.Batches++
	switch {
	case held > 2*j.cfg.TargetHold && j.batch > j.cfg.MinBatch:
		j.batch = max(j.batch/2, j.cfg.MinBatch)
	case held < j.cfg.TargetHold/2 && j.batch < j.cfg.Batch:
		j.batch = min(j.batch*2, j.cfg.Batch)
	}
	if hold.Contended {
		stats.Backoffs++
		j.backoff = min(max(2*j.backoff, j.cfg.Pause), j.cfg.MaxBackoff)
	} else {
		j.backoff /= 2
	}
	if j.cfg.Pause == 0 {
		return ctx.Err()
	}
	if !j.sleepFn(ctx, max(j.cfg.Pause, time.Duration(j.cfg.HoldRatio)*held)+j.backoff) {
		return ctx.Err()
	}
	return nil
}

func (j *Janitor) optimize(ctx context.Context, stats *Stats) error {
	for step := 0; step < maxVacuumSteps; step++ {
		result, err := j.store.OptimizeStorage(ctx, j.cfg.VacuumPages)
		if err != nil {
			return err
		}
		stats.Optimized = true
		stats.FreedPages += result.FreedPages
		if !result.IncrementalVacuum || result.FreedPages < j.cfg.VacuumPages {
			return nil
		}
		if !j.sleep(ctx, j.cfg.Pause) {
			return ctx.Err()
		}
	}
	return nil
}

func (j *Janitor) sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
