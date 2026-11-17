package sqlite

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// TestMigrationReviewFocusLoadsOldRows guards B743: review runs and queued
// triggers recorded before the focus columns existed still load, with no focus.
func TestMigrationReviewFocusLoadsOldRows(t *testing.T) {
	db := openMigratedDatabaseCopyNoForeignKeys(t, 20261115110000000)
	if _, err := db.Exec(`INSERT INTO review_run (id, review_id, session_id, harness, pr_url, target_sha, status, verdict, body, created_at)
		VALUES ('r-old', 'rev-1', 's1', 'claude-code', 'pr1', 'sha1', 'complete', 'approved', '', '2026-11-01T00:00:00Z')`); err != nil {
		t.Fatalf("seed old run: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO review_trigger_queue (session_id, pr_url, harness, created_at, next_attempt_at)
		VALUES ('s1', 'pr1', 'claude-code', '2026-11-01T00:00:00Z', '2026-11-01T00:00:00Z')`); err != nil {
		t.Fatalf("seed old queue entry: %v", err)
	}
	upTo(t, db, 20261116100000000)

	q := gen.New(db)
	runs, err := q.ListReviewRunsBySession(context.Background(), "s1")
	if err != nil || len(runs) != 1 {
		t.Fatalf("old run after migration = %v, %v; want one row", runs, err)
	}
	if runs[0].Focus != "" || runs[0].FocusAuthor != "" {
		t.Fatalf("old run focus = %q/%q, want empty", runs[0].Focus, runs[0].FocusAuthor)
	}
	queued, err := q.ListQueuedReviewTriggers(context.Background())
	if err != nil || len(queued) != 1 {
		t.Fatalf("old queue entry after migration = %v, %v; want one row", queued, err)
	}
	if queued[0].Focus != "" || queued[0].FocusAuthor != "" {
		t.Fatalf("old queue focus = %q/%q, want empty", queued[0].Focus, queued[0].FocusAuthor)
	}
	downTo(t, db, 20261115110000000)
}
