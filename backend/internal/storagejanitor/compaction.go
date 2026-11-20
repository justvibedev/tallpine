package storagejanitor

import (
	"context"

	sqlitestore "tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// holds remembers, per conversation whose last compaction pass ended with a
// held delta, the hold mark read before that pass. A held delta waits for a
// later event, a turn to settle or its session to end; until the mark shows
// one of those, scanning the conversation again would only hold it again.
// The map lives in memory: after a restart each held conversation is scanned
// once more and remembered again.
type holds map[string]sqlitestore.ProviderEventHoldMark

func (j *Janitor) compact(ctx context.Context, stats *Stats) error {
	candidates, err := j.store.ProviderEventCompactionCandidates(ctx)
	if err != nil {
		return err
	}
	previous := j.holds
	j.holds = holds{}
	for i, candidate := range candidates {
		mark, err := j.store.ProviderEventHoldMark(ctx, candidate.ConversationID)
		if err != nil {
			j.keepHolds(previous, candidates[i:])
			return err
		}
		if held, ok := previous[candidate.ConversationID]; ok && held == mark {
			j.holds[candidate.ConversationID] = mark
			stats.HoldsSkipped++
			continue
		}
		watermark, after := candidate.CompactedThroughID, candidate.CompactedThroughID
		for j.budgetLeft(stats) {
			var result sqlitestore.ProviderEventCompaction
			if err := j.paced(ctx, stats, func(limit int64) (sqlitestore.WriterHold, error) {
				var err error
				result, err = j.store.CompactProviderEvents(ctx, candidate.ConversationID, watermark, after, limit, j.cfg.Clock().UTC())
				return result.WriterHold, err
			}); err != nil {
				j.keepHolds(previous, candidates[i+1:])
				return err
			}
			stats.Deleted += result.Deleted
			stats.Keyed += result.Keyed
			if result.Done {
				if result.Holding {
					j.holds[candidate.ConversationID] = mark
				}
				break
			}
			watermark, after = result.Watermark, result.Next
		}
		if !j.budgetLeft(stats) {
			j.keepHolds(previous, candidates[i+1:])
			return nil
		}
	}
	return nil
}

// keepHolds carries the remembered holds of candidates a pass did not reach
// over to the next pass.
func (j *Janitor) keepHolds(previous holds, rest []sqlitestore.ConversationCompactionCandidate) {
	for _, candidate := range rest {
		if mark, ok := previous[candidate.ConversationID]; ok {
			j.holds[candidate.ConversationID] = mark
		}
	}
}
