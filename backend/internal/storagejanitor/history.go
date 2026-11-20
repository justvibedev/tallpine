package storagejanitor

import (
	"context"
	"time"

	sqlitestore "tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// HistoryStore is the store surface for pruning conversation history below a
// settled compaction boundary.
type HistoryStore interface {
	HistoryRetentionCandidates(ctx context.Context) ([]sqlitestore.HistoryRetentionCandidate, error)
	SettledHistoryBoundary(ctx context.Context, conversationID string, after int64) (int64, time.Time, error)
	PruneHistoryEvents(ctx context.Context, conversationID string, boundary, after, limit int64, now time.Time) (sqlitestore.HistoryEventPrune, error)
	PruneHistoryActivities(ctx context.Context, conversationID string, boundary int64, boundaryAt time.Time, watermark, after, limit int64, now time.Time) (sqlitestore.HistoryActivityPrune, error)
}

// HistoryStats reports one pass's history pruning.
type HistoryStats struct {
	// HistoryDeleted and HistoryKeyed count provider events pruned below a
	// compaction boundary; HistoryActivities counts activities shortened there.
	HistoryDeleted    int64
	HistoryKeyed      int64
	HistoryActivities int64
}

// pruneHistory bounds every compacted conversation's stored history.
//
// Compaction shrinks the model's context, not the database. Once a compaction
// has settled (a later turn started), the provider events below it lose their
// payloads and the activities created before it keep only a short head of
// their long texts; messages and turns stay whole. See
// sqlitestore.PruneHistoryEvents and PruneHistoryActivities for what is kept
// and why. A conversation that never compacts is left alone.
func (j *Janitor) pruneHistory(ctx context.Context, stats *Stats) error {
	candidates, err := j.store.HistoryRetentionCandidates(ctx)
	if err != nil {
		return err
	}
	for _, candidate := range candidates {
		if !j.budgetLeft(stats) {
			return nil
		}
		boundary, boundaryAt, err := j.store.SettledHistoryBoundary(ctx, candidate.ConversationID, candidate.BoundaryEventID)
		if err != nil {
			return err
		}
		if boundary == 0 {
			continue
		}
		if candidate.EventsPrunedThroughID < boundary {
			if err := j.pruneHistoryEvents(ctx, stats, candidate.ConversationID, boundary, candidate.EventsPrunedThroughID); err != nil {
				return err
			}
		}
		if candidate.ActivitiesDoneBoundaryID < boundary {
			if err := j.pruneHistoryActivities(ctx, stats, candidate, boundary, boundaryAt); err != nil {
				return err
			}
		}
	}
	return nil
}

func (j *Janitor) pruneHistoryEvents(ctx context.Context, stats *Stats, conversationID string, boundary, after int64) error {
	for j.budgetLeft(stats) {
		var result sqlitestore.HistoryEventPrune
		if err := j.paced(ctx, stats, func(limit int64) (sqlitestore.WriterHold, error) {
			var err error
			result, err = j.store.PruneHistoryEvents(ctx, conversationID, boundary, after, limit, j.cfg.Clock().UTC())
			return result.WriterHold, err
		}); err != nil {
			return err
		}
		stats.HistoryDeleted += result.Deleted
		stats.HistoryKeyed += result.Keyed
		if result.Done {
			return nil
		}
		after = result.Next
	}
	return nil
}

func (j *Janitor) pruneHistoryActivities(
	ctx context.Context,
	stats *Stats,
	candidate sqlitestore.HistoryRetentionCandidate,
	boundary int64,
	boundaryAt time.Time,
) error {
	watermark, after := candidate.ActivitiesPrunedThroughSeq, candidate.ActivitiesPrunedThroughSeq
	for j.budgetLeft(stats) {
		var result sqlitestore.HistoryActivityPrune
		if err := j.paced(ctx, stats, func(limit int64) (sqlitestore.WriterHold, error) {
			var err error
			result, err = j.store.PruneHistoryActivities(ctx, candidate.ConversationID, boundary, boundaryAt,
				watermark, after, limit, j.cfg.Clock().UTC())
			return result.WriterHold, err
		}); err != nil {
			return err
		}
		stats.HistoryActivities += result.Pruned
		if result.Done {
			return nil
		}
		watermark, after = result.Watermark, result.Next
	}
	return nil
}
