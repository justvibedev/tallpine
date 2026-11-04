package sqlite

import (
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
)

const (
	beforeBacklogCDC = int64(20261027195851059)
	backlogCDC       = int64(20261027203307205)
)

// schemaOutsideChangeLog maps every schema object not on change_log (triggers,
// indexes, tables, views) to its exact sqlite_master text.
func schemaOutsideChangeLog(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	rows, err := db.Query(`SELECT type, name, tbl_name, COALESCE(sql, '') FROM sqlite_master
		WHERE tbl_name <> 'change_log' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var typ, name, tbl, text string
		if err := rows.Scan(&typ, &name, &tbl, &text); err != nil {
			t.Fatal(err)
		}
		out[typ+" "+name+" on "+tbl] = text
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// changeLogShape is change_log's columns (type, nullability, default, pk) and
// foreign keys, so a rebuild cannot silently tighten or drop what an earlier
// migration (e.g. 0140 made project_id nullable) changed in place.
func changeLogShape(t *testing.T, db *sql.DB) string {
	t.Helper()
	return strings.Join(append(changeLogColumns(t, db), changeLogForeignKeys(t, db)...), "\n")
}

func changeLogColumns(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT cid, name, type, "notnull", COALESCE(dflt_value, '<none>'), pk, hidden FROM pragma_table_xinfo('change_log') ORDER BY cid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var cid, notnull, pk, hidden int
		var name, typ, dflt string
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk, &hidden); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprint("col ", cid, name, typ, notnull, dflt, pk, hidden))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func changeLogForeignKeys(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT "from", "table", "to", on_update, on_delete FROM pragma_foreign_key_list('change_log') ORDER BY "from"`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a, b, c, d, e string
		if err := rows.Scan(&a, &b, &c, &d, &e); err != nil {
			t.Fatal(err)
		}
		out = append(out, "fk "+a+" "+b+" "+c+" "+d+" "+e)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func gooseTo(t *testing.T, db *sql.DB, up bool, version int64) {
	t.Helper()
	gooseMu.Lock()
	defer gooseMu.Unlock()
	goose.SetBaseFS(migrationsFS)
	defer goose.SetBaseFS(nil)
	var err error
	if up {
		err = goose.UpTo(db, "migrations", version)
	} else {
		err = goose.DownTo(db, "migrations", version)
	}
	if err != nil {
		t.Fatalf("goose to %d (up=%t): %v", version, up, err)
	}
}

func diffSchema(before, after map[string]string) (added, removed, changed []string) {
	for k, v := range after {
		old, ok := before[k]
		switch {
		case !ok:
			added = append(added, k)
		case old != v:
			changed = append(changed, k)
		}
	}
	for k := range before {
		if _, ok := after[k]; !ok {
			removed = append(removed, k)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	sort.Strings(changed)
	return
}

func seedChangeLog(t *testing.T, db *sql.DB, n int) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO projects (id, path, registered_at) VALUES ('p', '/tmp/p', CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < ?)
		INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
		SELECT 'p', NULL, CASE i % 2 WHEN 0 THEN 'pr_updated' ELSE 'review_run_updated' END,
		       json_object('i', i), datetime('now', '-' || i || ' seconds') FROM n`, n); err != nil {
		t.Fatal(err)
	}
}

func changeLogSequence(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var seq int64
	if err := db.QueryRow(`SELECT COALESCE((SELECT seq FROM sqlite_sequence WHERE name = 'change_log'), 0)`).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	return seq
}

func changeLogDump(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT seq, COALESCE(project_id, '<nil>'), COALESCE(session_id, '<nil>'), event_type, payload, created_at FROM change_log ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var seq int64
		var a, b, c, d, e string
		if err := rows.Scan(&seq, &a, &b, &c, &d, &e); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprint(seq, a, b, c, d, e))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// The rebuild must not drop, recreate or rewrite any trigger or index outside
// change_log: later migrations redefined many of them, and restoring an older
// body would silently regress their CDC.
func TestBacklogCDCMigrationPreservesSchema(t *testing.T) {
	db := openMigratedDatabaseCopy(t, beforeBacklogCDC)
	before := schemaOutsideChangeLog(t, db)
	shapeBefore := changeLogShape(t, db)
	gooseTo(t, db, true, backlogCDC)
	after := schemaOutsideChangeLog(t, db)
	if shapeAfter := changeLogShape(t, db); shapeAfter != shapeBefore {
		t.Fatalf("change_log columns or foreign keys changed:\nbefore:\n%s\nafter:\n%s", shapeBefore, shapeAfter)
	}

	added, removed, changed := diffSchema(before, after)
	wantAdded := []string{
		"trigger backlog_items_cdc_insert on backlog_items",
		"trigger backlog_items_cdc_update on backlog_items",
	}
	if strings.Join(added, "|") != strings.Join(wantAdded, "|") || len(removed) > 0 || len(changed) > 0 {
		t.Fatalf("schema outside change_log changed: added=%v removed=%v changed=%v", added, removed, changed)
	}
	if len(before) < 100 {
		t.Fatalf("suspiciously small schema snapshot (%d objects): comparison would prove nothing", len(before))
	}
	for _, idx := range []string{"idx_change_log_project", "idx_change_log_created_at_seq"} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ? AND tbl_name = 'change_log'`, idx).Scan(&n); err != nil || n != 1 {
			t.Fatalf("index %s missing after rebuild: n=%d err=%v", idx, n, err)
		}
	}
	if _, err := db.Exec(`INSERT INTO change_log (project_id, session_id, event_type, payload) VALUES (NULL, NULL, 'any_future_event', '{}')`); err != nil {
		t.Fatalf("rebuilt change_log must accept a projectless row and any event type: %v", err)
	}
}

// Rows survive byte for byte and the AUTOINCREMENT high-water mark survives
// even when retention already pruned the newest rows; otherwise seq restarts
// below what SSE clients hold as Last-Event-ID.
func TestBacklogCDCMigrationKeepsRowsAndSequence(t *testing.T) {
	db := openMigratedDatabaseCopy(t, beforeBacklogCDC)
	seedChangeLog(t, db, 20)
	if _, err := db.Exec(`DELETE FROM change_log WHERE seq > 15`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE change_log SET project_id = NULL WHERE seq = 3`); err != nil {
		t.Fatal(err)
	}
	rowsBefore, seqBefore := changeLogDump(t, db), changeLogSequence(t, db)
	if seqBefore != 20 || len(rowsBefore) != 15 {
		t.Fatalf("seed: seq=%d rows=%d", seqBefore, len(rowsBefore))
	}
	gooseTo(t, db, true, backlogCDC)
	if got := changeLogDump(t, db); strings.Join(got, "\n") != strings.Join(rowsBefore, "\n") {
		t.Fatalf("rows changed by rebuild:\nbefore %v\nafter  %v", rowsBefore, got)
	}
	if got := changeLogSequence(t, db); got != seqBefore {
		t.Fatalf("sqlite_sequence = %d, want %d", got, seqBefore)
	}
	res, err := db.Exec(`INSERT INTO change_log (project_id, session_id, event_type, payload) VALUES ('p', NULL, 'backlog_changed', '{}')`)
	if err != nil {
		t.Fatalf("new event type rejected: %v", err)
	}
	if id, _ := res.LastInsertId(); id != seqBefore+1 {
		t.Fatalf("next seq = %d, want %d (reused a pruned seq)", id, seqBefore+1)
	}
}

// Existing CDC keeps firing after the rebuild: a sessions trigger and a
// project-fan-out trigger, both on the real migrated schema.
func TestBacklogCDCMigrationKeepsExistingTriggersFiring(t *testing.T) {
	db := openMigratedDatabaseCopy(t, beforeBacklogCDC)
	for _, q := range []string{
		`INSERT INTO projects (id, path, registered_at) VALUES ('p', '/tmp/p', CURRENT_TIMESTAMP)`,
		`INSERT INTO sessions (id, project_id, num, activity_last_at, created_at, updated_at)
		 VALUES ('s', 'p', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	gooseTo(t, db, true, backlogCDC)
	count := func(where string, args ...any) int {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM change_log WHERE `+where, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	base := count(`1 = 1`)
	if _, err := db.Exec(`UPDATE sessions SET stale_binary_since = CURRENT_TIMESTAMP WHERE id = 's'`); err != nil {
		t.Fatal(err)
	}
	if got := count(`session_id = 's' AND event_type = 'session_updated'`); got != 1 || count(`1 = 1`) != base+1 {
		t.Fatalf("session trigger: %d matching rows, total delta %d", got, count(`1 = 1`)-base)
	}
	if _, err := db.Exec(`INSERT INTO agent_model_catalog (agent_id, project_id, catalog_json, source, fetched_at) VALUES ('a', 'p', '{}', 't', CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if got := count(`event_type = 'session_updated' AND json_extract(payload, '$.kind') = 'model_catalog'`); got != 1 {
		t.Fatalf("model catalog trigger rows = %d, want 1", got)
	}
}

func changeLogSQL(t *testing.T, db *sql.DB) string {
	t.Helper()
	var text string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'change_log'`).Scan(&text); err != nil {
		t.Fatal(err)
	}
	return text
}

// A database with no sqlite_sequence row for change_log (never inserted into)
// must not end with a NULL seq after the rebuild.
func TestBacklogCDCMigrationWithoutSequenceRow(t *testing.T) {
	db := openMigratedDatabaseCopy(t, beforeBacklogCDC)
	if _, err := db.Exec(`DELETE FROM sqlite_sequence WHERE name = 'change_log'`); err != nil {
		t.Fatal(err)
	}
	gooseTo(t, db, true, backlogCDC)
	var nulls int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_sequence WHERE name = 'change_log' AND seq IS NULL`).Scan(&nulls); err != nil || nulls != 0 {
		t.Fatalf("sqlite_sequence has a NULL change_log seq: n=%d err=%v", nulls, err)
	}
	if _, err := db.Exec(`INSERT INTO projects (id, path, registered_at) VALUES ('p', '/tmp/p', CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	res, err := db.Exec(`INSERT INTO change_log (project_id, session_id, event_type, payload) VALUES ('p', NULL, 'backlog_changed', '{}')`)
	if err != nil {
		t.Fatal(err)
	}
	if id, _ := res.LastInsertId(); id != 1 {
		t.Fatalf("first seq = %d, want 1", id)
	}
	gooseTo(t, db, false, beforeBacklogCDC)
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_sequence WHERE name = 'change_log' AND seq IS NULL`).Scan(&nulls); err != nil || nulls != 0 {
		t.Fatalf("down left a NULL change_log seq: n=%d err=%v", nulls, err)
	}
}

func TestBacklogCDCMigrationDownRestoresCheck(t *testing.T) {
	db := openMigratedDatabaseCopy(t, beforeBacklogCDC)
	sqlBefore := changeLogSQL(t, db)
	seedChangeLog(t, db, 5)
	before := schemaOutsideChangeLog(t, db)
	shapeBefore := changeLogShape(t, db)
	rowsBefore, seqBefore := changeLogDump(t, db), changeLogSequence(t, db)

	gooseTo(t, db, true, backlogCDC)
	if _, err := db.Exec(`INSERT INTO backlog_items (id, project_id, seq, title, state, position, origin_kind, created_at, updated_at)
		VALUES ('i1', 'p', 1, 't', 'inbox', 1, 'human', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	gooseTo(t, db, false, beforeBacklogCDC)

	if added, removed, changed := diffSchema(before, schemaOutsideChangeLog(t, db)); len(added)+len(removed)+len(changed) > 0 {
		t.Fatalf("down left schema drift: added=%v removed=%v changed=%v", added, removed, changed)
	}
	if got := changeLogSQL(t, db); got != sqlBefore {
		t.Fatalf("down did not restore change_log DDL byte for byte:\nbefore: %s\nafter:  %s", sqlBefore, got)
	}
	if got := changeLogShape(t, db); got != shapeBefore {
		t.Fatalf("down changed change_log shape:\nbefore:\n%s\nafter:\n%s", shapeBefore, got)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM change_log WHERE event_type = 'backlog_changed'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("backlog_changed rows after down: %d %v", n, err)
	}
	if got := changeLogDump(t, db); strings.Join(got, "\n") != strings.Join(rowsBefore, "\n") {
		t.Fatalf("rows changed by up+down:\nbefore %v\nafter  %v", rowsBefore, got)
	}
	if got := changeLogSequence(t, db); got < seqBefore {
		t.Fatalf("sequence regressed on down: %d < %d", got, seqBefore)
	}
	if _, err := db.Exec(`INSERT INTO change_log (project_id, session_id, event_type, payload) VALUES ('p', NULL, 'backlog_changed', '{}')`); err == nil {
		t.Fatal("event_type CHECK not restored by down")
	}
}

// Rebuild cost on a large log, for the PR description. Opt-in:
// CHANGELOG_REBUILD_TIMING_ROWS=100000.
func TestBacklogCDCMigrationRebuildTiming(t *testing.T) {
	var rows int
	if _, err := fmt.Sscan(os.Getenv("CHANGELOG_REBUILD_TIMING_ROWS"), &rows); err != nil || rows == 0 {
		t.Skip("set CHANGELOG_REBUILD_TIMING_ROWS to time the rebuild")
	}
	db := openMigratedDatabaseCopy(t, beforeBacklogCDC)
	seedChangeLog(t, db, rows)
	start := time.Now()
	gooseTo(t, db, true, backlogCDC)
	t.Logf("rebuild of %d change_log rows took %s", rows, time.Since(start))
}
