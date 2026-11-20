package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// Provider-event identity keys (B832).
//
// The archive's unique (conversation_id, provider_event_id) index makes a
// replayed provider event a no-op. When compaction or retention removes a row,
// its identity must keep refusing replays, so it moves to
// conversation_provider_event_keys and the row is deleted outright, instead of
// staying behind as a '{}' tombstone with its four index entries. Most removed
// rows need no key at all: one without an identity is never deduplicated, and
// a persistent host's journal identity (acp-host:<launch>:<n>) can only be
// replayed by the host process that minted it, from its in-memory replay list.
// That stops once a later turn started, because the host refuses a new prompt
// until the previous one was acknowledged, and once the session is terminated,
// because terminating destroys the host; a new host truncates the journal and
// mints identities under a new random launch token.

// retiredEvent is one archive row compaction or retention removes.
type retiredEvent struct {
	ID              int64
	ConversationID  string
	ProviderEventID string
	// SessionTerminated reports that the session that wrote the row is
	// terminated, so its persistent host is gone.
	SessionTerminated bool
}

// retireProviderEvents deletes rows, keeping the identity of every row a
// replay could still send. It reports rows deleted without a key and rows
// whose identity was kept. latestStart caches each conversation's newest
// turn.started id; it may be nil or already hold ids the caller read in the
// same transaction. Call it inside the caller's transaction.
func retireProviderEvents(ctx context.Context, q *gen.Queries, rows []retiredEvent, latestStart map[string]int64) (deleted, keyed int64, err error) {
	if len(rows) == 0 {
		return 0, 0, nil
	}
	if latestStart == nil {
		latestStart = map[string]int64{}
	}
	ids := make([]int64, 0, len(rows))
	var keep []int64
	for _, row := range rows {
		ids = append(ids, row.ID)
		if row.ProviderEventID == "" {
			continue
		}
		if persistentHostEvent(row.ProviderEventID) && row.SessionTerminated {
			continue
		}
		if persistentHostEvent(row.ProviderEventID) {
			started, ok := latestStart[row.ConversationID]
			if !ok {
				if started, err = q.LatestProviderTurnStartedID(ctx, row.ConversationID); err != nil {
					return 0, 0, err
				}
				latestStart[row.ConversationID] = started
			}
			if started > row.ID {
				continue
			}
		}
		keep = append(keep, row.ID)
	}
	if len(keep) > 0 {
		if _, err := q.KeepProviderEventKeysByID(ctx, keep); err != nil {
			return 0, 0, err
		}
	}
	n, err := q.DeleteProviderEventsByID(ctx, ids)
	if err != nil {
		return 0, 0, err
	}
	keyed = int64(len(keep))
	return n - keyed, keyed, nil
}

// persistentHostEvent reports a persistent host's journal identity, which the
// host stops replaying once Tallpine acknowledged its prompt.
func persistentHostEvent(providerEventID string) bool {
	return strings.HasPrefix(providerEventID, persistentHostEventPrefix)
}

// TombstoneSweep reports one window of the one-time tombstone sweep.
type TombstoneSweep struct {
	WriterHold
	Deleted int64
	Keyed   int64
	// Done reports that the sweep has covered every id that could hold a
	// tombstone; it never needs to run again.
	Done bool
}

// SweepProviderEventTombstones turns up to limit archive rows above the
// persisted sweep cursor that are '{}' tombstones, left by compaction and
// retention before identity keys existed, into keys and deletes them.
func (s *Store) SweepProviderEventTombstones(ctx context.Context, limit int64, now time.Time) (TombstoneSweep, error) {
	release, err := s.holdWriter(ctx)
	if err != nil {
		return TombstoneSweep{}, err
	}
	var result TombstoneSweep
	err = s.inTx(ctx, "sweep provider event tombstones", func(q *gen.Queries) error {
		cursor, err := q.GetProviderEventTombstoneSweep(ctx)
		if err != nil {
			return err
		}
		if cursor.SweptThroughID >= cursor.ThroughID {
			result.Done = true
			return nil
		}
		rows, err := q.SelectProviderEventsForTombstoneSweep(ctx, gen.SelectProviderEventsForTombstoneSweepParams{
			AfterID:   cursor.SweptThroughID,
			ThroughID: cursor.ThroughID,
			PageLimit: limit,
		})
		if err != nil {
			return err
		}
		next := cursor.ThroughID
		if int64(len(rows)) == limit {
			next = rows[len(rows)-1].ID
		}
		var retired []retiredEvent
		for _, row := range rows {
			if row.Tombstone != 0 {
				retired = append(retired, retiredEvent{
					ID: row.ID, ConversationID: row.ConversationID, ProviderEventID: row.ProviderEventID,
					SessionTerminated: row.SessionTerminated != 0,
				})
			}
		}
		if result.Deleted, result.Keyed, err = retireProviderEvents(ctx, q, retired, nil); err != nil {
			return err
		}
		result.Done = next >= cursor.ThroughID
		return q.AdvanceProviderEventTombstoneSweep(ctx, gen.AdvanceProviderEventTombstoneSweepParams{
			SweptThroughID: next,
			UpdatedAt:      now,
		})
	})
	result.WriterHold = release()
	return result, err
}

// ProviderEventHoldMark is what compaction's decision to hold a delta depends
// on. While a conversation's mark is unchanged, a window that held a delta
// would hold it again, so the janitor skips the conversation.
type ProviderEventHoldMark struct {
	LatestEventID      int64
	Turns              int64
	OpenTurns          int64
	CurrentSessionID   string
	TerminatedSessions int64
}

// ProviderEventHoldMark reads a conversation's hold mark from the read pool,
// without taking the writer.
func (s *Store) ProviderEventHoldMark(ctx context.Context, conversationID string) (ProviderEventHoldMark, error) {
	row, err := s.qr.GetProviderEventHoldMark(ctx, conversationID)
	if err != nil {
		return ProviderEventHoldMark{}, fmt.Errorf("read provider event hold mark: %w", err)
	}
	return ProviderEventHoldMark(row), nil
}
