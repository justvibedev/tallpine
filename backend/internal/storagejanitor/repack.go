package storagejanitor

import (
	"context"
	"time"

	sqlitestore "tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

const (
	// DefaultRepackCheckEvery paces the archive slack measurement. It reads
	// every page of the archive table once (about 25 s of reads on a 2.5 GB
	// archive, no write lock), so it runs at most daily.
	DefaultRepackCheckEvery = 24 * time.Hour
	// DefaultRepackMinUnused is the least unused space inside the archive's
	// pages worth a repack sweep.
	DefaultRepackMinUnused int64 = 128 << 20
	// DefaultRepackMinUnusedPercent is the least share of the archive's pages
	// that must be unused for a sweep. A freshly packed b-tree keeps some
	// slack, so a sweep does not start again right after one finished.
	DefaultRepackMinUnusedPercent int64 = 25
)

// RepackStore is the store surface for returning the archive's space: the
// one-time tombstone sweep and the repack.
type RepackStore interface {
	SweepProviderEventTombstones(ctx context.Context, limit int64, now time.Time) (sqlitestore.TombstoneSweep, error)
	ProviderEventArchiveSlack(ctx context.Context) (sqlitestore.ProviderEventArchiveSlack, error)
	RepackProviderEvents(ctx context.Context, after, through, limit int64) (sqlitestore.ProviderEventRepack, error)
}

// RepackConfig customizes the archive repack. Zero values use the defaults.
type RepackConfig struct {
	CheckEvery       time.Duration
	MinUnused        int64
	MinUnusedPercent int64
}

// RepackStats reports one pass's archive repack.
type RepackStats struct {
	// RepackMeasured reports that the pass measured the archive's slack.
	RepackMeasured bool
	// RepackStarted reports that the measurement started a sweep.
	RepackStarted bool
	// Repacked counts archive rows rewritten into packed pages.
	Repacked int64
	// RepackFailed reports that a measurement or window failed and ended the
	// sweep; the rest of the pass still ran.
	RepackFailed bool
	// TombstonesSwept counts tombstones from before identity keys that the
	// sweep deleted, with or without keeping a key.
	TombstonesSwept int64
}

// repackState is the sweep in progress, kept across passes. A restart drops
// it; the next measurement starts a new sweep only if enough slack is left.
type repackState struct {
	swept        bool
	sweepRetryAt time.Time
	lastCheck    time.Time
	active       bool
	cursor       int64
	through      int64
}

func (c RepackConfig) withDefaults() RepackConfig {
	if c.CheckEvery <= 0 {
		c.CheckEvery = DefaultRepackCheckEvery
	}
	if c.MinUnused <= 0 {
		c.MinUnused = DefaultRepackMinUnused
	}
	if c.MinUnusedPercent <= 0 {
		c.MinUnusedPercent = DefaultRepackMinUnusedPercent
	}
	return c
}

// repack returns the space compaction and retention freed inside the
// archive's pages. Tombstoning shrinks rows in place, which incremental vacuum
// cannot reclaim, so the file would keep its high-water mark forever and a
// full VACUUM holds the writer for tens of seconds on a large database. A
// sweep rewrites the archive in id order, one short paced transaction at a
// time (see sqlitestore.RepackProviderEvents); the pages it frees are returned
// by the next incremental vacuum.
//
// The one-time tombstone sweep runs first; a repack waits for it, because the
// sweep frees whole pages that incremental vacuum returns without a rewrite.
// A repack sweep starts only after a pass that drained its archive backlog, so it
// does not rewrite rows that are about to be removed, and only when the
// measured slack is large in bytes and in share. It covers the ids that
// existed when it was measured.
func (j *Janitor) repack(ctx context.Context, stats *Stats) error {
	err := j.repackStep(ctx, stats)
	if err == nil || ctx.Err() != nil {
		return err
	}
	// A repack only returns space; it must never keep the passes after it
	// (incremental vacuum among them) from running. A window that fails ends
	// the sweep; the next daily measurement may start a new one.
	j.repackState.active = false
	stats.RepackFailed = true
	j.cfg.Logger.Warn("provider event archive repack stopped", "err", err, "cursor", j.repackState.cursor)
	return nil
}

func (j *Janitor) repackStep(ctx context.Context, stats *Stats) error {
	if done, err := j.sweepTombstones(ctx, stats); err != nil || !done {
		return err
	}
	if !j.repackState.active {
		if stats.OutOfBudget || !j.repackDue() {
			return nil
		}
		if err := j.measureRepack(ctx, stats); err != nil || !j.repackState.active {
			return err
		}
	}
	for j.budgetLeft(stats) {
		var result sqlitestore.ProviderEventRepack
		if err := j.paced(ctx, stats, func(limit int64) (sqlitestore.WriterHold, error) {
			var err error
			result, err = j.store.RepackProviderEvents(ctx, j.repackState.cursor, j.repackState.through, limit)
			return result.WriterHold, err
		}); err != nil {
			return err
		}
		stats.Repacked += result.Rewritten
		j.repackState.cursor = result.Next
		if result.Done {
			j.repackState.active = false
			return nil
		}
	}
	return nil
}

// sweepTombstones deletes the '{}' tombstones compaction and retention wrote
// before identity keys existed (see sqlitestore.SweepProviderEventTombstones),
// and reports whether the sweep is finished. It is persisted, so it runs once.
func (j *Janitor) sweepTombstones(ctx context.Context, stats *Stats) (bool, error) {
	if j.cfg.Clock().Before(j.repackState.sweepRetryAt) {
		return false, nil
	}
	for !j.repackState.swept && j.budgetLeft(stats) {
		var result sqlitestore.TombstoneSweep
		if err := j.paced(ctx, stats, func(limit int64) (sqlitestore.WriterHold, error) {
			var err error
			result, err = j.store.SweepProviderEventTombstones(ctx, limit, j.cfg.Clock().UTC())
			return result.WriterHold, err
		}); err != nil {
			// Retried after CheckEvery, not every pass.
			j.repackState.sweepRetryAt = j.cfg.Clock().Add(j.cfg.Repack.CheckEvery)
			return false, err
		}
		stats.TombstonesSwept += result.Deleted + result.Keyed
		j.repackState.swept = result.Done
	}
	return j.repackState.swept, nil
}

func (j *Janitor) repackDue() bool {
	last := j.repackState.lastCheck
	return last.IsZero() || j.cfg.Clock().Sub(last) >= j.cfg.Repack.CheckEvery
}

func (j *Janitor) measureRepack(ctx context.Context, stats *Stats) error {
	j.repackState.lastCheck = j.cfg.Clock()
	slack, err := j.store.ProviderEventArchiveSlack(ctx)
	if err != nil {
		return err
	}
	stats.RepackMeasured = true
	if slack.UnusedBytes < j.cfg.Repack.MinUnused || slack.UnusedBytes*100 < slack.Bytes*j.cfg.Repack.MinUnusedPercent {
		return nil
	}
	stats.RepackStarted = true
	j.repackState.active, j.repackState.cursor, j.repackState.through = true, 0, slack.MaxID
	j.cfg.Logger.Info("repacking the provider event archive to return the space inside its pages",
		"archiveBytes", slack.Bytes, "unusedBytes", slack.UnusedBytes, "throughID", slack.MaxID)
	return nil
}
