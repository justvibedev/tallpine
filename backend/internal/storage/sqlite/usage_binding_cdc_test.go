package sqlite

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// TestUsageBindingsCDCTriggerWatchesEveryFact keeps the update trigger's WHEN
// list in step with the table: a column added later and left out of it would
// change without telling the renderer. updated_at is the one deliberate
// exclusion (refreshing it alone is not a change), and id never changes.
func TestUsageBindingsCDCTriggerWatchesEveryFact(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "ao.db")+pragmas)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	var trigger string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'trigger' AND name = 'usage_bindings_cdc_update'`).Scan(&trigger); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(`SELECT name FROM pragma_table_info('usage_bindings')`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			t.Fatal(err)
		}
		watched := strings.Contains(trigger, "OLD."+column+" IS NOT NEW."+column)
		switch column {
		case "id", "updated_at":
			if watched {
				t.Errorf("usage_bindings_cdc_update watches %s, which is not a binding fact", column)
			}
		default:
			if !watched {
				t.Errorf("usage_bindings_cdc_update does not watch usage_bindings.%s", column)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
