package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// Session retention (B161) purges a terminated session's raw provider events
// and, when asked, its transcript. Each batch takes the writer lock on its own
// and releases it, so projection writes never queue behind a whole purge.
// Usage, model usage, turns, 'system' activities, reviews and PRs are never
// touched here.

// RetentionFootprint is what a session's purgeable data takes in the database.
// Bytes are payload lengths, an estimate of the space a purge frees.
type RetentionFootprint struct {
	ProviderEvents      int64
	ProviderEventBytes  int64
	TranscriptRows      int64
	TranscriptRowsBytes int64
}

// SessionRetentionFootprint measures a session's purgeable data.
func (s *Store) SessionRetentionFootprint(ctx context.Context, id domain.SessionID) (RetentionFootprint, error) {
	events, err := s.qr.SessionProviderEventFootprint(ctx, id)
	if err != nil {
		return RetentionFootprint{}, fmt.Errorf("measure provider events of %s: %w", id, err)
	}
	transcript, err := s.qr.SessionTranscriptFootprint(ctx, &id)
	if err != nil {
		return RetentionFootprint{}, fmt.Errorf("measure transcript of %s: %w", id, err)
	}
	return RetentionFootprint{
		ProviderEvents: events.RowsCount, ProviderEventBytes: events.Bytes,
		TranscriptRows: transcript.RowsCount, TranscriptRowsBytes: transcript.Bytes,
	}, nil
}

// retentionBatch bounds the rows one purge transaction deletes.
const retentionBatch = 500

// purgeBatches runs batch until it deletes fewer than retentionBatch rows,
// each call in its own transaction under its own writer hold.
func (s *Store) purgeBatches(ctx context.Context, what string, batch func(*gen.Queries) (int64, error)) (int64, error) {
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		release, err := s.holdWriter(ctx)
		if err != nil {
			return total, err
		}
		var n int64
		err = s.inTx(ctx, what, func(q *gen.Queries) error {
			var err error
			n, err = batch(q)
			return err
		})
		release()
		if err != nil {
			return total, err
		}
		total += n
		if n < retentionBatch {
			return total, nil
		}
	}
}

// PurgeSessionProviderEvents deletes every archived provider event of a
// session, tombstones included, and its expiry cursor.
func (s *Store) PurgeSessionProviderEvents(ctx context.Context, id domain.SessionID) (int64, error) {
	n, err := s.purgeBatches(ctx, "purge provider events", func(q *gen.Queries) (int64, error) {
		return q.DeleteSessionProviderEventsBatch(ctx, gen.DeleteSessionProviderEventsBatchParams{SessionID: id, PageLimit: retentionBatch})
	})
	if err != nil {
		return n, fmt.Errorf("purge provider events of %s: %w", id, err)
	}
	err = s.inTx(ctx, "purge provider event cursor", func(q *gen.Queries) error {
		return q.DeleteSessionProviderEventRetentionCursor(ctx, string(id))
	})
	return n, err
}

// PurgeSessionTranscript deletes a session-scoped conversation's messages, its
// non-'system' activities, the terminal turns and the last assistant message.
// Turns and 'system' activities (compaction markers) stay.
func (s *Store) PurgeSessionTranscript(ctx context.Context, id domain.SessionID) (int64, error) {
	var total int64
	steps := []struct {
		what  string
		batch func(*gen.Queries) (int64, error)
	}{
		{"purge messages", func(q *gen.Queries) (int64, error) {
			return q.DeleteSessionMessagesBatch(ctx, gen.DeleteSessionMessagesBatchParams{SessionID: &id, PageLimit: retentionBatch})
		}},
		{"purge activities", func(q *gen.Queries) (int64, error) {
			return q.DeleteSessionActivitiesBatch(ctx, gen.DeleteSessionActivitiesBatchParams{SessionID: &id, PageLimit: retentionBatch})
		}},
		{"purge terminal turns", func(q *gen.Queries) (int64, error) {
			return q.DeleteSessionTerminalTurnsBatch(ctx, gen.DeleteSessionTerminalTurnsBatchParams{SessionID: string(id), PageLimit: retentionBatch})
		}},
	}
	for _, step := range steps {
		n, err := s.purgeBatches(ctx, step.what, step.batch)
		total += n
		if err != nil {
			return total, fmt.Errorf("purge transcript of %s: %w", id, err)
		}
	}
	err := s.inTx(ctx, "purge last assistant message", func(q *gen.Queries) error {
		return q.DeleteSessionLastAssistantMessage(ctx, string(id))
	})
	return total, err
}

// MarkSessionRetentionPurge records what retention purged from a session. A
// later mark never clears an earlier one.
func (s *Store) MarkSessionRetentionPurge(ctx context.Context, id domain.SessionID, events, transcript bool, now time.Time) error {
	return s.inTx(ctx, "mark retention purge", func(q *gen.Queries) error {
		return q.UpsertSessionRetentionPurge(ctx, gen.UpsertSessionRetentionPurgeParams{
			SessionID: string(id), PurgedAt: now.UTC(),
			ProviderEvents: boolInt(events), Transcripts: boolInt(transcript),
		})
	})
}

// SessionRetentionPurged reports whether retention purged a session's provider
// events or transcript.
func (s *Store) SessionRetentionPurged(ctx context.Context, id domain.SessionID) (bool, error) {
	row, err := s.qr.GetSessionRetentionPurge(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read retention purge of %s: %w", id, err)
	}
	return row.ProviderEvents == 1 || row.Transcripts == 1, nil
}
