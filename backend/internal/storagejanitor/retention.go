package storagejanitor

import (
	"context"
	"time"

	sqlitestore "tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// expire deletes the provider events of sessions terminated
// longer ago than the retention window, one session cursor at a time.
func (j *Janitor) expire(ctx context.Context, retention time.Duration, stats *Stats) error {
	before := j.cfg.Clock().UTC().Add(-retention)
	sessions, err := j.store.ExpiredSessionsWithProviderEvents(ctx, before, expiredSessionPage)
	if err != nil {
		return err
	}
	for _, session := range sessions {
		after := session.ExpiredThroughID
		for j.budgetLeft(stats) {
			var result sqlitestore.ProviderEventExpiry
			if err := j.paced(ctx, stats, func(limit int64) (sqlitestore.WriterHold, error) {
				var err error
				result, err = j.store.ExpireSessionProviderEvents(ctx, session.SessionID, after, limit, j.cfg.Clock().UTC())
				return result.WriterHold, err
			}); err != nil {
				return err
			}
			stats.ExpiredDeleted += result.Deleted
			stats.ExpiredKeyed += result.Keyed
			if result.Done {
				break
			}
			after = result.Next
		}
		if !j.budgetLeft(stats) {
			return nil
		}
	}
	return nil
}
