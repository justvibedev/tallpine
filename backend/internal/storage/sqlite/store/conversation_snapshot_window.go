package store

import (
	"sort"

	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// snapshotPageWindow computes the oldest sequence a page reaches back to and
// whether older rows remain, from the over-fetched message and activity rows.
func snapshotPageWindow(messageRows []gen.ConversationMessage, activityRows []gen.ConversationActivity, visibleAfterSequence, limit, beforeSequence int64) (oldest int64, hasMore bool) {
	sequences := make([]int64, 0, len(messageRows)+len(activityRows))
	for _, row := range messageRows {
		if row.Sequence > visibleAfterSequence {
			sequences = append(sequences, row.Sequence)
		}
	}
	for _, row := range activityRows {
		if row.Sequence > visibleAfterSequence {
			sequences = append(sequences, row.Sequence)
		}
	}
	sort.Slice(sequences, func(i, j int) bool { return sequences[i] > sequences[j] })
	hasMore = int64(len(sequences)) > limit
	if int64(len(sequences)) > limit {
		sequences = sequences[:limit]
	}
	oldest = beforeSequence
	if len(sequences) > 0 {
		oldest = sequences[len(sequences)-1]
	}
	if len(sequences) == 0 || oldest < visibleAfterSequence {
		oldest = visibleAfterSequence
	}
	if oldest <= visibleAfterSequence {
		hasMore = false
	}
	return oldest, hasMore
}
