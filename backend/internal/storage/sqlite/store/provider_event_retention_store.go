package store

import (
	"context"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// Provider-event archive compaction and retention.
//
// The archive is written once per provider event and read back only to
// deduplicate replays (the unique provider_event_id index) and to prove a turn
// completed (SelectVerifiedAuthenticationTurn reads turn.completed). The
// timeline renders from the projected messages, activities and turns, never
// from the archive. Streamed deltas are therefore dead weight once their turn
// has settled: the message or activity they were folded into holds the text.
//
// Every method here does one bounded unit of work under the writer lock and
// returns, so the janitor can pause between units and projection writes never
// queue behind a long cleanup transaction.

// ConversationCompactionCandidate is a conversation with archived events above
// its compaction watermark.
type ConversationCompactionCandidate struct {
	ConversationID     string
	CompactedThroughID int64
}

// ProviderEventCompaction reports one compaction window.
type ProviderEventCompaction struct {
	WriterHold
	Scanned    int
	Deleted    int64
	Tombstoned int64
	// Watermark is the conversation's persisted compaction watermark after the
	// window: every row at or below it is done. A held delta keeps it below
	// itself.
	Watermark int64
	// Next is where the following window starts: the last row this one looked
	// at. It moves past held deltas, so a hold never stops later rows from
	// being compacted.
	Next int64
	// Done reports that the window reached the end of the conversation's
	// archive.
	Done bool
}

// streamedDeltaMethod reports the archive methods that carry only streamed text
// already folded into a projected message or activity.
func streamedDeltaMethod(method string) bool {
	switch method {
	case "message.delta", "reasoning.delta", "command.output.delta", "command.input", "activity.text":
		return true
	}
	return false
}

// ProviderEventCompactionCandidates lists conversations with archived events
// the janitor has not looked at yet.
func (s *Store) ProviderEventCompactionCandidates(ctx context.Context) ([]ConversationCompactionCandidate, error) {
	rows, err := s.qr.ListProviderEventCompactionCandidates(ctx)
	if err != nil {
		return nil, fmt.Errorf("list provider event compaction candidates: %w", err)
	}
	out := make([]ConversationCompactionCandidate, 0, len(rows))
	for _, row := range rows {
		out = append(out, ConversationCompactionCandidate{
			ConversationID:     row.ConversationID,
			CompactedThroughID: row.CompactedThroughID,
		})
	}
	return out, nil
}

// CompactProviderEvents compacts one window of up to limit archived events
// above after in a conversation. watermark is the conversation's current
// watermark; after is the window cursor, equal to it on a pass's first window
// and ahead of it once an earlier window held a delta, in which case the
// watermark stays where it is.
//
// A streamed delta whose turn has settled is removed. A delta with no native
// identity is deleted. A delta with one is reduced to a '{}' tombstone instead:
// the identity must stay in the dedupe index, because a persistent host replays
// an unacknowledged prompt from its start and a native-history import replays
// with stable identities, and a delta let through again would append its text
// to the settled message a second time.
//
// A delta of a turn Tallpine has no row for (a nested provider thread, or a turn from
// before a restart) counts as settled once any later turn.completed is archived
// in the conversation, wherever that is relative to the window. A delta of a turn
// still queued or running, or an unattributed one newer than every completion,
// stays: it holds the persisted watermark below itself so a later pass looks at
// it again, while Next lets the caller carry on past it.
func (s *Store) CompactProviderEvents(
	ctx context.Context,
	conversationID string,
	watermark, after, limit int64,
	now time.Time,
) (ProviderEventCompaction, error) {
	release, err := s.holdWriter(ctx)
	if err != nil {
		return ProviderEventCompaction{Watermark: watermark, Next: after}, err
	}
	result, err := s.compactProviderEventsLocked(ctx, conversationID, watermark, after, limit, now)
	result.WriterHold = release()
	return result, err
}

func (s *Store) compactProviderEventsLocked(
	ctx context.Context,
	conversationID string,
	watermark, after, limit int64,
	now time.Time,
) (ProviderEventCompaction, error) {
	result := ProviderEventCompaction{Watermark: watermark, Next: after}

	err := s.inTx(ctx, "compact provider events", func(q *gen.Queries) error {
		rows, err := q.SelectProviderEventsForCompaction(ctx, gen.SelectProviderEventsForCompactionParams{
			ConversationID: conversationID,
			AfterID:        after,
			PageLimit:      limit,
		})
		if err != nil {
			return err
		}
		result.Scanned = len(rows)
		if len(rows) == 0 {
			result.Done = true
			return nil
		}
		latestCompleted, err := q.LatestProviderTurnCompletedID(ctx, conversationID)
		if err != nil {
			return err
		}
		var deletes, tombstones []int64
		remove := func(row gen.SelectProviderEventsForCompactionRow) {
			if row.ProviderEventID == "" {
				deletes = append(deletes, row.ID)
			} else {
				tombstones = append(tombstones, row.ID)
			}
		}
		var held int64
		for _, row := range rows {
			if !streamedDeltaMethod(row.Method) || row.Compacted != 0 {
				continue
			}
			state := domain.TurnState(row.TurnState)
			if state.Terminal() || (state == "" && row.ID < latestCompleted) {
				remove(row)
				continue
			}
			if held == 0 {
				held = row.ID
			}
		}
		result.Next = rows[len(rows)-1].ID
		switch {
		case watermark < after:
			// An earlier window of this pass held a delta.
		case held != 0:
			result.Watermark = max(watermark, held-1)
		default:
			result.Watermark = result.Next
		}
		result.Done = int64(len(rows)) < limit
		if len(deletes) > 0 {
			if result.Deleted, err = q.DeleteProviderEventsByID(ctx, deletes); err != nil {
				return err
			}
		}
		if len(tombstones) > 0 {
			if result.Tombstoned, err = q.TombstoneProviderEventsByID(ctx, tombstones); err != nil {
				return err
			}
		}
		if result.Watermark > watermark {
			return q.UpsertProviderEventCompactionWatermark(ctx, gen.UpsertProviderEventCompactionWatermarkParams{
				ConversationID:     conversationID,
				CompactedThroughID: result.Watermark,
				UpdatedAt:          now,
			})
		}
		return nil
	})
	return result, err
}

// ExpiredSession is a session past retention with archived events above its
// expiry cursor.
type ExpiredSession struct {
	SessionID        domain.SessionID
	ExpiredThroughID int64
}

// ProviderEventExpiry reports one retention window.
type ProviderEventExpiry struct {
	WriterHold
	Deleted    int64
	Tombstoned int64
	// Next is the session's expiry cursor after the window.
	Next int64
	// Done reports that the window reached the end of the session's archive.
	Done bool
}

// ExpiredSessionsWithProviderEvents lists up to limit sessions terminated
// before the cutoff that still have archived events to expire.
func (s *Store) ExpiredSessionsWithProviderEvents(ctx context.Context, before time.Time, limit int64) ([]ExpiredSession, error) {
	rows, err := s.qr.ListExpiredTerminatedSessionsWithProviderEvents(ctx,
		gen.ListExpiredTerminatedSessionsWithProviderEventsParams{Before: before, PageLimit: limit})
	if err != nil {
		return nil, fmt.Errorf("list expired sessions with provider events: %w", err)
	}
	out := make([]ExpiredSession, 0, len(rows))
	for _, row := range rows {
		out = append(out, ExpiredSession{SessionID: row.SessionID, ExpiredThroughID: row.ExpiredThroughID})
	}
	return out, nil
}

// ExpireSessionProviderEvents expires one window of up to limit archived events
// of a session past retention, above the session's expiry cursor after.
//
// Rows without a native identity are deleted. Rows with one, turn.completed
// included, are reduced to '{}' tombstones for the same reason compaction keeps
// them: a terminated session can be restored, and its native-history import or
// a persistent-host replay re-sends those identities. Without the row, a
// replayed delta would append its text to the settled message a second time.
func (s *Store) ExpireSessionProviderEvents(
	ctx context.Context,
	session domain.SessionID,
	after, limit int64,
	now time.Time,
) (ProviderEventExpiry, error) {
	release, err := s.holdWriter(ctx)
	if err != nil {
		return ProviderEventExpiry{Next: after}, err
	}
	result, err := s.expireSessionProviderEventsLocked(ctx, session, after, limit, now)
	result.WriterHold = release()
	return result, err
}

func (s *Store) expireSessionProviderEventsLocked(
	ctx context.Context,
	session domain.SessionID,
	after, limit int64,
	now time.Time,
) (ProviderEventExpiry, error) {
	result := ProviderEventExpiry{Next: after}
	err := s.inTx(ctx, "expire provider events", func(q *gen.Queries) error {
		rows, err := q.SelectSessionProviderEventsForRetention(ctx, gen.SelectSessionProviderEventsForRetentionParams{
			SessionID: session,
			AfterID:   after,
			PageLimit: limit,
		})
		if err != nil {
			return err
		}
		result.Done = int64(len(rows)) < limit
		if len(rows) == 0 {
			return nil
		}
		var deletes, tombstones []int64
		for _, row := range rows {
			if row.ProviderEventID == "" {
				deletes = append(deletes, row.ID)
			} else {
				tombstones = append(tombstones, row.ID)
			}
		}
		if len(deletes) > 0 {
			if result.Deleted, err = q.DeleteProviderEventsByID(ctx, deletes); err != nil {
				return err
			}
		}
		if len(tombstones) > 0 {
			if result.Tombstoned, err = q.TombstoneProviderEventsByID(ctx, tombstones); err != nil {
				return err
			}
		}
		result.Next = rows[len(rows)-1].ID
		return q.UpsertSessionProviderEventRetention(ctx, gen.UpsertSessionProviderEventRetentionParams{
			SessionID:        string(session),
			ExpiredThroughID: result.Next,
			UpdatedAt:        now,
		})
	})
	if err != nil {
		return ProviderEventExpiry{Next: after}, fmt.Errorf("expire provider events of %s: %w", session, err)
	}
	return result, nil
}

// StorageMaintenance reports one OptimizeStorage pass.
type StorageMaintenance struct {
	// IncrementalVacuum reports that the database uses auto_vacuum=INCREMENTAL.
	IncrementalVacuum bool
	// FreedPages is how many free pages incremental vacuum returned to the OS.
	FreedPages int64
}

// OptimizeStorage runs PRAGMA optimize and, only on a database created with
// auto_vacuum=INCREMENTAL, returns up to maxVacuumPages free pages to the OS.
// It never runs a full VACUUM: that rewrites the whole file and blocks every
// writer for as long as it takes.
func (s *Store) OptimizeStorage(ctx context.Context, maxVacuumPages int64) (StorageMaintenance, error) {
	var result StorageMaintenance
	if err := s.writeMu.LockContext(ctx); err != nil {
		return result, err
	}
	defer s.writeMu.Unlock()
	if _, err := s.writeDB.ExecContext(ctx, "PRAGMA optimize"); err != nil {
		return result, fmt.Errorf("pragma optimize: %w", err)
	}
	var mode int64
	if err := s.writeDB.QueryRowContext(ctx, "PRAGMA auto_vacuum").Scan(&mode); err != nil {
		return result, fmt.Errorf("read auto_vacuum: %w", err)
	}
	const incremental = 2
	if mode != incremental || maxVacuumPages <= 0 {
		return result, nil
	}
	result.IncrementalVacuum = true
	var before, after int64
	if err := s.writeDB.QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&before); err != nil {
		return result, fmt.Errorf("read freelist: %w", err)
	}
	if before == 0 {
		return result, nil
	}
	if err := s.incrementalVacuum(ctx, maxVacuumPages); err != nil {
		return result, fmt.Errorf("incremental vacuum: %w", err)
	}
	if err := s.writeDB.QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&after); err != nil {
		return result, fmt.Errorf("read freelist: %w", err)
	}
	result.FreedPages = before - after
	return result, nil
}

// incrementalVacuum frees up to maxPages free pages. incremental_vacuum frees
// one page per step, so the statement is drained rather than trusting Exec to
// step it to the end. The rows are closed before returning, so the caller can
// query the write connection again.
func (s *Store) incrementalVacuum(ctx context.Context, maxPages int64) error {
	rows, err := s.writeDB.QueryContext(ctx, fmt.Sprintf("PRAGMA incremental_vacuum(%d)", maxPages))
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
	}
	return rows.Err()
}
