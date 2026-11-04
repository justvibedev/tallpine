package sqlite

import (
	"database/sql"
	"testing"
)

// TestFullyMigratedSchemaAllowsDeepSeekHarness guards the sessions.harness CHECK
// on the schema a fresh database ends at, not just the historical migration:
// DeepSeek Harness was removed from the product and restored later, and no
// migration in between may have narrowed the constraint.
func TestFullyMigratedSchemaAllowsDeepSeekHarness(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", databaseURI(dir)+pragmas)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('p', '/tmp/p', CURRENT_TIMESTAMP)`)
	insert := `INSERT INTO sessions (id, project_id, num, harness, created_at, updated_at, activity_last_at) VALUES (?, 'p', ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`
	if _, err := db.Exec(insert, "s1", 1, "deepseek-harness"); err != nil {
		t.Fatalf("deepseek-harness rejected by the current sessions schema: %v", err)
	}
	if _, err := db.Exec(insert, "s2", 2, "not-a-harness"); err == nil {
		t.Fatal("unknown harness bypassed the CHECK constraint")
	}
}
