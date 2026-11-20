package store

import (
	"context"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// Provider-event archive repack (B832).
//
// Compaction and retention reduce archive rows to '{}' tombstones in place.
// The space a shrunk row gave up stays inside its b-tree page, so the pages
// never become free and incremental vacuum has nothing to return: on one
// 5 GB database 913 MB of the archive's 2.5 GB was unused space inside pages.
// A repack deletes a window of consecutive rows and inserts the same rows
// again in one transaction, which packs them into full pages and frees the
// rest. Every column, the id included, is copied in SQL through a temporary
// table, so no value is converted on the way and no row changes.

// repackWindowBytes bounds the stored payload one repack window copies, so a
// run of large activity payloads still makes a short transaction.
const repackWindowBytes int64 = 4 << 20

// repackRowOverhead approximates a row's columns other than its payload.
const repackRowOverhead int64 = 256

// The temporary table lives on the single writer connection and outlives the
// transaction; IF NOT EXISTS recreates it after the connection is replaced.
// Its columns are declared without a type, so they apply no affinity and every
// value passes through exactly as stored.
//
// repackRowsSQL picks the rows of a window that are copied and deleted; both
// statements use it, so they always agree. A row with no branch is left in
// place: conversation_provider_events_branch_insert would assign it the active
// branch when it is inserted again. So is a row whose conversation, session or
// review no longer exists (migrations run with foreign keys off): inserting it
// again would fail its foreign key and stop every later window.
const (
	repackColumns = `id, conversation_id, session_id, provider_event_id, method, payload_json, received_at, branch_id, review_id`
	repackRowsSQL = `main.conversation_provider_events.id > ? AND main.conversation_provider_events.id <= ?
  AND main.conversation_provider_events.branch_id <> ''
  AND EXISTS (SELECT 1 FROM main.conversations c WHERE c.id = main.conversation_provider_events.conversation_id)
  AND EXISTS (SELECT 1 FROM main.sessions s WHERE s.id = main.conversation_provider_events.session_id)
  AND (main.conversation_provider_events.review_id IS NULL
       OR EXISTS (SELECT 1 FROM main.review r WHERE r.id = main.conversation_provider_events.review_id))`
	repackPrepareSQL = `CREATE TEMP TABLE IF NOT EXISTS provider_event_repack (` + repackColumns + `)`
	repackClearSQL   = `DELETE FROM temp.provider_event_repack`
	repackCopySQL    = `INSERT INTO temp.provider_event_repack (` + repackColumns + `)
SELECT ` + repackColumns + ` FROM main.conversation_provider_events WHERE ` + repackRowsSQL + ` ORDER BY id`
	repackDeleteSQL  = `DELETE FROM main.conversation_provider_events WHERE ` + repackRowsSQL
	repackRestoreSQL = `INSERT INTO main.conversation_provider_events (` + repackColumns + `)
SELECT ` + repackColumns + ` FROM temp.provider_event_repack ORDER BY id`
)

// ProviderEventArchiveSlack is the provider-event archive's b-tree footprint.
type ProviderEventArchiveSlack struct {
	// Bytes is every page the archive table occupies, overflow pages included.
	Bytes int64
	// UnusedBytes is the space inside those pages that holds no row.
	UnusedBytes int64
	// MaxID is the newest archived id when the footprint was read.
	MaxID int64
}

// ProviderEventArchiveSlack measures the archive table with dbstat on the
// reader pool. It reads every page of the table once and takes no write lock,
// so run it rarely.
func (s *Store) ProviderEventArchiveSlack(ctx context.Context) (ProviderEventArchiveSlack, error) {
	var slack ProviderEventArchiveSlack
	err := s.readDB.QueryRowContext(ctx, `SELECT
    CAST(COALESCE(SUM(pgsize), 0) AS INTEGER),
    CAST(COALESCE(SUM(unused), 0) AS INTEGER),
    (SELECT CAST(COALESCE(MAX(id), 0) AS INTEGER) FROM conversation_provider_events)
FROM dbstat('main', 1) WHERE name = 'conversation_provider_events'`).Scan(&slack.Bytes, &slack.UnusedBytes, &slack.MaxID)
	if err != nil {
		return slack, fmt.Errorf("measure provider event archive: %w", err)
	}
	return slack, nil
}

// ProviderEventRepack reports one repack window.
type ProviderEventRepack struct {
	WriterHold
	// Rewritten counts rows deleted and inserted again.
	Rewritten int64
	// Next is the last id the window covered; the following window starts
	// after it.
	Next int64
	// Done reports that the window reached through.
	Done bool
}

// RepackProviderEvents rewrites the archive rows with after < id <= through,
// at most limit of them and at most repackWindowBytes of payload, as one
// transaction under the writer lock. A row with no branch is left in place.
func (s *Store) RepackProviderEvents(ctx context.Context, after, through, limit int64) (ProviderEventRepack, error) {
	release, err := s.holdWriter(ctx)
	if err != nil {
		return ProviderEventRepack{Next: after}, err
	}
	result, err := s.repackProviderEventsLocked(ctx, after, through, limit)
	result.WriterHold = release()
	return result, err
}

func (s *Store) repackProviderEventsLocked(ctx context.Context, after, through, limit int64) (ProviderEventRepack, error) {
	result := ProviderEventRepack{Next: after}
	err := s.inTxDB(ctx, "repack provider events", func(q *gen.Queries, db gen.DBTX) error {
		if db == nil {
			return fmt.Errorf("repack needs its own transaction")
		}
		rows, err := q.SelectProviderEventSizesForRepack(ctx, gen.SelectProviderEventSizesForRepackParams{
			AfterID:   after,
			ThroughID: through,
			PageLimit: limit,
		})
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			result.Next, result.Done = through, true
			return nil
		}
		last, taken := repackWindowEnd(rows)
		if err := repackWindow(ctx, db, after, last); err != nil {
			return err
		}
		result.Next = last
		result.Done = taken == len(rows) && int64(len(rows)) < limit
		if result.Done {
			result.Next = through
		}
		result.Rewritten, err = repackedRows(ctx, db)
		return err
	})
	return result, err
}

// repackWindowEnd is the last id that fits the byte budget and how many rows
// that takes. The first row is always taken, so a single huge row still moves
// the cursor.
func repackWindowEnd(rows []gen.SelectProviderEventSizesForRepackRow) (int64, int) {
	var bytes int64
	for i, row := range rows {
		bytes += row.PayloadBytes + repackRowOverhead
		if i > 0 && bytes > repackWindowBytes {
			return rows[i-1].ID, i
		}
	}
	return rows[len(rows)-1].ID, len(rows)
}

// repackWindow moves the window's rows to the temporary table, deletes them
// from the archive and inserts them back in id order. The caller's
// transaction is on the writer connection, so the temporary table is too.
func repackWindow(ctx context.Context, db gen.DBTX, after, last int64) error {
	for _, stmt := range []struct {
		what string
		sql  string
		args []any
	}{
		{"prepare", repackPrepareSQL, nil},
		{"clear", repackClearSQL, nil},
		{"copy", repackCopySQL, []any{after, last}},
		{"delete", repackDeleteSQL, []any{after, last}},
		{"restore", repackRestoreSQL, nil},
	} {
		if _, err := db.ExecContext(ctx, stmt.sql, stmt.args...); err != nil {
			return fmt.Errorf("repack %s: %w", stmt.what, err)
		}
	}
	return nil
}

// repackedRows counts the rows the window rewrote and empties the temporary
// table, so it never holds a copy of archive rows between transactions.
func repackedRows(ctx context.Context, db gen.DBTX) (int64, error) {
	var n int64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM temp.provider_event_repack`).Scan(&n); err != nil {
		return 0, fmt.Errorf("repack count: %w", err)
	}
	if _, err := db.ExecContext(ctx, repackClearSQL); err != nil {
		return 0, fmt.Errorf("repack clear: %w", err)
	}
	return n, nil
}
