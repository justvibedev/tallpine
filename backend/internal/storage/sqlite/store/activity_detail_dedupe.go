package store

import (
	"context"
	"encoding/json"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// compactActivityDetail drops what a stored activity detail repeats (B842;
// domain.DropActivityDetailRepeats): the output keys the command_output column
// shadows once it holds streamed output, and a rawCommand equal to command.
// ACP providers re-send the whole tool call on completion, output included,
// which kept every command's output twice.
//
// A detail with nothing to drop, or one that is not a JSON object, is
// returned unchanged, byte for byte. The same input always yields the same
// output, so an unchanged provider resend still compares equal to the row.
// Native-history reconciliation compares a row with its replay by
// domain.ActivityDetailDigest, which drops the same repeats before hashing.
//
// The SQL backfill (CompactActivityDetailsWindow) applies the same rules to
// rows written before this existed.
func compactActivityDetail(detail string, outputStreamed bool) string {
	if !domain.ActivityDetailMayRepeat([]byte(detail), outputStreamed) {
		return detail
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(detail), &fields) != nil || fields == nil ||
		!domain.DropActivityDetailRepeats(fields, outputStreamed) {
		return detail
	}
	compacted, err := json.Marshal(fields)
	if err != nil {
		return detail
	}
	return string(compacted)
}

// ActivityDetailCompaction reports one window of the one-time activity detail
// backfill.
type ActivityDetailCompaction struct {
	WriterHold
	// Compacted counts detail rewrites: one per repeat dropped, so a row that
	// repeated both its output and its command counts twice.
	Compacted int64
	// Done reports that the backfill covered every row that existed when it
	// was installed; it never needs to run again.
	Done bool
}

// CompactActivityDetailsWindow rewrites up to limit activity rows above the
// persisted backfill cursor without the repeats compactActivityDetail drops,
// for rows written before it existed (B842). Rows are visited in rowid order
// up to the newest rowid that existed when the migration installed the
// cursor; every later row was written compacted. The revision is left alone:
// what a client is shown does not change, so there is nothing to refetch.
func (s *Store) CompactActivityDetailsWindow(ctx context.Context, limit int64, now time.Time) (ActivityDetailCompaction, error) {
	release, err := s.holdWriter(ctx)
	if err != nil {
		return ActivityDetailCompaction{}, err
	}
	var result ActivityDetailCompaction
	err = s.inTx(ctx, "compact activity details", func(q *gen.Queries) error {
		cursor, err := q.GetActivityDetailCompaction(ctx)
		if err != nil {
			return err
		}
		if cursor.CompactedThroughRowid >= cursor.ThroughRowid {
			result.Done = true
			return nil
		}
		next, err := q.ActivityDetailCompactionWindowEnd(ctx, gen.ActivityDetailCompactionWindowEndParams{
			AfterRowid:   cursor.CompactedThroughRowid,
			ThroughRowid: cursor.ThroughRowid,
			LastOffset:   max(limit-1, 0),
		})
		if err != nil {
			return err
		}
		if result.Compacted, err = q.CompactActivityDetailOutput(ctx, gen.CompactActivityDetailOutputParams{
			AfterRowid: cursor.CompactedThroughRowid, ThroughRowid: next,
		}); err != nil {
			return err
		}
		raw, err := q.CompactActivityDetailRawCommand(ctx, gen.CompactActivityDetailRawCommandParams{
			AfterRowid: cursor.CompactedThroughRowid, ThroughRowid: next,
		})
		if err != nil {
			return err
		}
		result.Compacted += raw
		result.Done = next >= cursor.ThroughRowid
		return q.AdvanceActivityDetailCompaction(ctx, gen.AdvanceActivityDetailCompactionParams{
			CompactedThroughRowid: next,
			UpdatedAt:             now,
		})
	})
	result.WriterHold = release()
	return result, err
}
