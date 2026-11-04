package sqlite

import (
	"reflect"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

// burnedVersion reports version numbers that must never be (re)used: they
// shipped in a release and were then deleted, so real installs have them
// recorded as applied while this repository ships no file with that number. A
// new file claiming one would be skipped silently there.
//
//   - 22 shipped in a nightly (#2412) and was deleted by the revert.
//
// Beware of the adjacent hazard this cannot catch: at least one field profile
// has versions 40 through 51 recorded as applied by a foreign build
// (#3475/#3476), so migrations numbered up to 0051 are skipped there entirely.
// Any such migration whose schema the generated queries depend on must add a
// schemaRepairs entry in db.go.
func burnedVersion(v int64) bool {
	// 203 was used by the historical Hermes candidate and must not be reused.
	return v == 22 || v == 203
}

// TestMigrationVersionLedger enforces the append-only migration ledger: every
// migration file has exactly one entry in shippedMigrations (adding a
// migration means appending its entry in the same change, where a review can
// see the claimed number), no shipped file is renamed or deleted, and no file
// reuses a burned number. Renumbering or deleting a released migration is what
// produces silently-skipped migrations and unexplainable 500s months later
// (issue #3475).
func TestMigrationVersionLedger(t *testing.T) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}

	present := map[int64]string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		version, err := goose.NumericComponent(e.Name())
		if err != nil {
			t.Errorf("migration %q has no version goose can parse: %v", e.Name(), err)
			continue
		}
		// Two files claiming one number is the failure this ledger exists to
		// prevent, and it is easy to reach from a branch: goose refuses to
		// start at all, so it takes down the daemon rather than one query.
		if other, clash := present[version]; clash {
			t.Errorf("migrations %q and %q both claim version %d: goose refuses to start on a duplicate version",
				other, e.Name(), version)
		}
		present[version] = e.Name()

		if burnedVersion(version) {
			t.Errorf("migration %q claims burned version %d: that number is recorded as applied on real installs, so this file would be skipped silently there; use a fresh number",
				e.Name(), version)
			continue
		}
		shipped, ok := shippedMigrations[version]
		switch {
		case ok && shipped != e.Name():
			t.Errorf("migration version %d was renamed from %q to %q: installs that already applied it will never run the new file", version, shipped, e.Name())
		case !ok:
			t.Errorf("migration %q (version %d) is not in the shippedMigrations ledger; append it in the same change so the claimed number is reviewed",
				e.Name(), version)
		}
	}
	for version, name := range shippedMigrations {
		if _, ok := present[version]; !ok {
			t.Errorf("ledgered migration %q (version %d) was deleted: installs that have not applied it yet will silently miss its schema, and the number is burned for reuse", name, version)
		}
	}
}

func TestReconcileMiMoHarnessConstraintAfterBurnedVersion(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 164)
	if _, err := db.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (165, 1)`); err != nil {
		t.Fatalf("seed burned MiMo migration: %v", err)
	}
	var schema string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(schema, "'mimo-code'") {
		t.Fatal("burned migration unexpectedly added MiMo Code")
	}
	if err := migrate(db); err != nil {
		t.Fatalf("migrate burned MiMo profile: %v", err)
	}
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(schema, "'fx'") || !strings.Contains(schema, "'gemini'") || !strings.Contains(schema, "'mimo-code'") {
		t.Fatalf("repaired sessions constraint lost fx, Gemini, or MiMo Code: %s", schema)
	}
	if err := reconcileHarnessConstraint(db); err != nil {
		t.Fatalf("repeat repair: %v", err)
	}
}

// A concurrently approved branch can claim the next free number first, so this
// branch's migrations have to survive being applied to a database that already
// records a version they never shipped. goose runs with WithAllowMissing, which
// is what makes the resulting gap harmless.
func TestMigrationsApplyOverAForeignInterleavedVersion(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 103)

	// Stand in for the other branch's migration: applied here, absent from this
	// tree, and numbered below everything this branch adds.
	if _, err := db.Exec(
		`INSERT INTO goose_db_version (version_id, is_applied) VALUES (104, 1)`,
	); err != nil {
		t.Fatalf("seed foreign migration: %v", err)
	}

	if err := migrate(db); err != nil {
		t.Fatalf("migrate over a foreign interleaved version: %v", err)
	}
	for table, wantColumns := range expectedUsageTableColumns {
		if got := tableColumns(t, db, table); !reflect.DeepEqual(got, wantColumns) {
			t.Errorf("%s columns = %v, want %v", table, got, wantColumns)
		}
	}
	var duplicates int
	if err := db.QueryRow(`
SELECT COUNT(*) FROM (
    SELECT version_id FROM goose_db_version GROUP BY version_id HAVING COUNT(*) > 1
)`).Scan(&duplicates); err != nil || duplicates != 0 {
		t.Fatalf("duplicate applied versions = %d, err = %v", duplicates, err)
	}
	if err := migrate(db); err != nil {
		t.Fatalf("repeat migration over a foreign interleaved version: %v", err)
	}
}
