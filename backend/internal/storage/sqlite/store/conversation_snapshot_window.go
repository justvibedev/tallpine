package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

// snapshotByteFloor returns the oldest sequence a page may reach when a table's
// byte budget cut its rows short, or 0 when no table was cut. A short result is
// ambiguous (exhausted or truncated), so probe once for an older row.
func (s *Store) snapshotByteFloor(
	ctx context.Context, conversationID string, visibleAfter, fetchLimit int64,
	messageRows []gen.ConversationMessage, activityRows []gen.ConversationActivity,
) (int64, error) {
	var floor int64
	consider := func(n int, oldest int64, probe func() (bool, error)) error {
		if int64(n) >= fetchLimit || n == 0 || oldest <= visibleAfter {
			return nil
		}
		more, err := probe()
		if err != nil {
			return err
		}
		if more && oldest > floor {
			floor = oldest
		}
		return nil
	}
	if n := len(messageRows); n > 0 {
		oldest := messageRows[n-1].Sequence
		if err := consider(n, oldest, func() (bool, error) {
			_, err := s.snapshotReader(ctx).HasConversationMessagesBefore(ctx, gen.HasConversationMessagesBeforeParams{
				ConversationID: conversationID, BeforeSequence: oldest,
			})
			return err == nil, ignoreNoRows(err)
		}); err != nil {
			return 0, fmt.Errorf("probe message page: %w", err)
		}
	}
	if n := len(activityRows); n > 0 {
		oldest := activityRows[n-1].Sequence
		if err := consider(n, oldest, func() (bool, error) {
			_, err := s.snapshotReader(ctx).HasConversationActivitiesBefore(ctx, gen.HasConversationActivitiesBeforeParams{
				ConversationID: conversationID, BeforeSequence: oldest,
			})
			return err == nil, ignoreNoRows(err)
		}); err != nil {
			return 0, fmt.Errorf("probe activity page: %w", err)
		}
	}
	return floor, nil
}

// conversationPageMaxBytes bounds the payload bytes one snapshot read loads per
// table. The newest row is always returned so a cursor can advance past a single
// oversized row.
const conversationPageMaxBytes = 4 << 20

// loadSnapshotPageWindow reads the over-fetched message and activity rows below
// beforeSequence and computes the page window over them.
func (s *Store) loadSnapshotPageWindow(
	ctx context.Context, conversationID string, beforeSequence, visibleAfter, limit int64,
) (messageRows []gen.ConversationMessage, activityRows []gen.ConversationActivity, oldest int64, hasMore bool, err error) {
	fetchLimit := limit + 1
	messageRows, err = s.snapshotReader(ctx).SelectConversationMessagesPage(ctx, gen.SelectConversationMessagesPageParams{
		ConversationID: conversationID, BeforeSequence: beforeSequence, PageLimit: fetchLimit, PageBytes: conversationPageMaxBytes,
	})
	if err != nil {
		return nil, nil, 0, false, fmt.Errorf("select message page: %w", err)
	}
	activityRows, err = s.snapshotReader(ctx).SelectConversationActivitiesPage(ctx, gen.SelectConversationActivitiesPageParams{
		ConversationID: conversationID, BeforeSequence: beforeSequence, PageLimit: fetchLimit, PageBytes: conversationPageMaxBytes,
	})
	if err != nil {
		return nil, nil, 0, false, fmt.Errorf("select activity page: %w", err)
	}
	oldest, hasMore = snapshotPageWindow(messageRows, activityRows, visibleAfter, limit, beforeSequence)
	// A byte-bounded read can stop short of the row limit with older rows left.
	// Keep the page from reaching past what that table actually returned.
	floor, err := s.snapshotByteFloor(ctx, conversationID, visibleAfter, fetchLimit, messageRows, activityRows)
	if err != nil {
		return nil, nil, 0, false, err
	}
	if floor > 0 && floor >= oldest {
		oldest, hasMore = floor, true
	}
	return messageRows, activityRows, oldest, hasMore, nil
}

func ignoreNoRows(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}
