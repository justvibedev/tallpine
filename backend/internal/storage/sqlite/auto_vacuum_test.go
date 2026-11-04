package sqlite

import (
	"database/sql"
	"testing"
)

func autoVacuumMode(t *testing.T, dir string) int {
	t.Helper()
	db, err := sql.Open("sqlite", databaseURI(dir)+readOnlyPragmas)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var mode int
	if err := db.QueryRow(`PRAGMA auto_vacuum`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	return mode
}

// A new database is created with incremental auto-vacuum; an existing one keeps
// whatever mode it was created with, because converting it would need a full
// VACUUM of the live database.
func TestOpenCreatesNewDatabasesWithIncrementalVacuum(t *testing.T) {
	fresh := t.TempDir()
	store, err := Open(fresh)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if mode := autoVacuumMode(t, fresh); mode != 2 {
		t.Fatalf("new database auto_vacuum = %d, want 2 (incremental)", mode)
	}

	existing := t.TempDir()
	legacy, err := sql.Open("sqlite", plainDSN(existing))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`CREATE TABLE predates_auto_vacuum (a)`); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(existing)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if mode := autoVacuumMode(t, existing); mode != 0 {
		t.Fatalf("existing database auto_vacuum = %d, want it left at 0", mode)
	}
}
