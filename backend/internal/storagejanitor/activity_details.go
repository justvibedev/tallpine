package storagejanitor

import (
	"context"
	"time"

	sqlitestore "tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// ActivityDetailStore is the store surface for the one-time backfill that
// drops what stored activity details repeat.
type ActivityDetailStore interface {
	CompactActivityDetailsWindow(ctx context.Context, limit int64, now time.Time) (sqlitestore.ActivityDetailCompaction, error)
}

// ActivityDetailStats reports one pass's activity detail backfill.
type ActivityDetailStats struct {
	// ActivityDetailsCompacted counts activity detail rewrites that dropped a
	// repeated command output or command.
	ActivityDetailsCompacted int64
}

// activityDetailState is the backfill's progress across passes. The cursor
// itself is persisted, so a restart only forgets that it already finished,
// which the first window then reports again.
type activityDetailState struct {
	done    bool
	retryAt time.Time
}

// compactActivityDetails works off the rows written before activity details
// were stored without their repeats (B842; see
// sqlitestore.CompactActivityDetailsWindow), one paced window at a time.
// Command output used to sit both in the detail and in the command_output
// column, about 40% of the activity table. Shrunk rows free their overflow
// pages for the incremental vacuum that follows. A failing window is retried
// after Repack.CheckEvery, not every pass, and never stops the rest of the
// pass.
func (j *Janitor) compactActivityDetails(ctx context.Context, stats *Stats) {
	state := &j.activityDetails
	if state.done || j.cfg.Clock().Before(state.retryAt) {
		return
	}
	for j.budgetLeft(stats) {
		var result sqlitestore.ActivityDetailCompaction
		if err := j.paced(ctx, stats, func(limit int64) (sqlitestore.WriterHold, error) {
			var err error
			result, err = j.store.CompactActivityDetailsWindow(ctx, limit, j.cfg.Clock().UTC())
			return result.WriterHold, err
		}); err != nil {
			if ctx.Err() == nil {
				state.retryAt = j.cfg.Clock().Add(j.cfg.Repack.CheckEvery)
				j.cfg.Logger.Warn("activity detail compaction stopped", "err", err)
			}
			return
		}
		stats.ActivityDetailsCompacted += result.Compacted
		if result.Done {
			state.done = true
			return
		}
	}
}
