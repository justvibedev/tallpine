package sqlite

import (
	"context"
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

var (
	sessionUpdatePattern   = regexp.MustCompile(`(?is)\bUPDATE\s+sessions\s+SET\b[^;]*`)
	revisionBumpPattern    = regexp.MustCompile(`(?i)\brevision\s*=\s*revision\s*\+\s*1\b`)
	rawSessionUpdateInCode = regexp.MustCompile(`(?i)\bUPDATE\s+sessions\s+SET\b`)
)

// Session CAS (hibernation, lifecycle writes) fences on sessions.revision, so
// every write must advance it. Writers do that in their own SET list rather
// than through a trigger that re-updated the row, which doubled every session
// write. A sessions UPDATE that forgets the bump silently breaks those fences,
// so every place one can come from is checked: sqlc queries, triggers in the
// migrated schema, and raw SQL in Go code.
func TestEverySessionUpdateBumpsRevision(t *testing.T) {
	queries, err := filepath.Glob("queries/*.sql")
	if err != nil || len(queries) == 0 {
		t.Fatalf("no queries found: %v", err)
	}
	updates := 0
	for _, path := range queries {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, stmt := range sessionUpdatePattern.FindAllString(string(data), -1) {
			updates++
			if !revisionBumpPattern.MatchString(stmt) {
				t.Errorf("%s: sessions UPDATE does not set revision = revision + 1:\n%s", path, stmt)
			}
		}
	}
	if updates == 0 {
		t.Fatal("found no sessions UPDATE statements; the pattern no longer matches the queries")
	}

	db := openMigratedTestDB(t)
	rows, err := db.Query(`SELECT name, sql FROM sqlite_master WHERE type = 'trigger' AND sql IS NOT NULL`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var name, body string
		if err := rows.Scan(&name, &body); err != nil {
			t.Fatal(err)
		}
		for _, stmt := range sessionUpdatePattern.FindAllString(body, -1) {
			if !revisionBumpPattern.MatchString(stmt) {
				t.Errorf("trigger %s updates sessions without advancing revision:\n%s", name, stmt)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	// A foreign-key action changes sessions rows without any statement of
	// ours, so it never advances revision. Each one must be preempted by an
	// explicit, bumping write before the parent row goes away.
	detachedBy := map[string]string{
		// automation_runs rows are only deleted by DeleteAutomation's cascade,
		// which runs DetachSessionsFromAutomation first.
		"automation_run_id": "DetachSessionsFromAutomation",
	}
	fks, err := db.Query(`SELECT "from", on_update, on_delete FROM pragma_foreign_key_list('sessions')`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fks.Close() }()
	for fks.Next() {
		var column, onUpdate, onDelete string
		if err := fks.Scan(&column, &onUpdate, &onDelete); err != nil {
			t.Fatal(err)
		}
		mutates := onDelete == "SET NULL" || onDelete == "SET DEFAULT" ||
			onUpdate == "CASCADE" || onUpdate == "SET NULL" || onUpdate == "SET DEFAULT"
		if !mutates {
			continue
		}
		query, ok := detachedBy[column]
		if !ok {
			t.Errorf("sessions.%s has ON UPDATE %s / ON DELETE %s, which rewrites sessions without advancing revision; detach it explicitly first and list it here", column, onUpdate, onDelete)
			continue
		}
		found := false
		for _, path := range queries {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "-- name: "+query+" ") {
				found = true
			}
		}
		if !found {
			t.Errorf("sessions.%s is detached by %s, which no query defines", column, query)
		}
	}
	if err := fks.Err(); err != nil {
		t.Fatal(err)
	}

	// Raw SQL outside sqlc would escape the query check above.
	root := filepath.Join("..", "..")
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "gen" && strings.HasSuffix(filepath.Dir(path), filepath.Join("storage", "sqlite")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if rawSessionUpdateInCode.Match(data) {
			t.Errorf("%s writes sessions with raw SQL; add an sqlc query that advances revision instead", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// One write is one revision step and one row write: no trigger re-updates the
// row, CAS still rejects a stale expectation, and a rolled back write leaves
// the revision alone.
func TestSessionWritesAdvanceRevisionOnce(t *testing.T) {
	db := openMigratedTestDB(t)
	db.SetMaxOpenConns(1) // BEGIN/ROLLBACK below must share a connection
	ctx := context.Background()
	q := gen.New(db)
	exec := func(query string) {
		t.Helper()
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	read := func(query string) int64 {
		t.Helper()
		var n int64
		if err := db.QueryRow(query).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	revision := func() int64 { return read(`SELECT revision FROM sessions WHERE id = 'rev-1'`) }
	exec(`INSERT INTO projects (id, path, registered_at) VALUES ('rev', '/rev', CURRENT_TIMESTAMP)`)
	exec(`INSERT INTO sessions (id, project_id, num, activity_last_at, created_at, updated_at)
		VALUES ('rev-1', 'rev', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	if n := read(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name = 'sessions_revision_update'`); n != 0 {
		t.Fatalf("sessions_revision_update still installed")
	}
	now := time.Now().UTC()

	writes := map[string]func() (int64, error){
		"UpdateSessionModel": func() (int64, error) {
			return q.UpdateSessionModel(ctx, gen.UpdateSessionModelParams{Model: "m", ID: "rev-1"})
		},
		"RecordSessionInteraction": func() (int64, error) {
			now = now.Add(time.Second)
			return q.RecordSessionInteraction(ctx, gen.RecordSessionInteractionParams{InteractionAt: sql.NullTime{Time: now, Valid: true}, ID: "rev-1"})
		},
		"RenameSession": func() (int64, error) {
			return q.RenameSession(ctx, gen.RenameSessionParams{DisplayName: "renamed " + now.String(), UpdatedAt: now, ID: "rev-1"})
		},
	}
	for name, write := range writes {
		before := revision()
		changes := read(`SELECT COUNT(*) FROM change_log`)
		if n, err := write(); err != nil || n != 1 {
			t.Fatalf("%s: rows=%d err=%v", name, n, err)
		}
		if got := revision(); got != before+1 {
			t.Fatalf("%s: revision = %d, want %d", name, got, before+1)
		}
		if name == "RenameSession" {
			if got := read(`SELECT COUNT(*) FROM change_log`); got != changes+1 {
				t.Fatalf("rename emitted %d CDC events, want 1", got-changes)
			}
		}
	}

	before := revision()
	if n, err := q.SetSessionHibernated(ctx, gen.SetSessionHibernatedParams{HibernatedAt: sql.NullTime{Time: now, Valid: true}, ID: "rev-1", ExpectedRevision: before - 1}); err != nil || n != 0 {
		t.Fatalf("stale CAS: rows=%d err=%v, want 0 rows", n, err)
	}
	if n, err := q.SetSessionHibernated(ctx, gen.SetSessionHibernatedParams{HibernatedAt: sql.NullTime{Time: now, Valid: true}, ID: "rev-1", ExpectedRevision: before}); err != nil || n != 1 {
		t.Fatalf("current CAS: rows=%d err=%v, want 1 row", n, err)
	}
	if got := revision(); got != before+1 {
		t.Fatalf("CAS write: revision = %d, want %d", got, before+1)
	}

	before = revision()
	exec(`BEGIN`)
	exec(`UPDATE sessions SET revision = revision + 1, display_name = 'rolled back' WHERE id = 'rev-1'`)
	exec(`ROLLBACK`)
	if got := revision(); got != before {
		t.Fatalf("rollback changed revision: %d, want %d", got, before)
	}
}
