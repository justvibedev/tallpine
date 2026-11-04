package migratevo

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

func TestRewriteDBPrefixOnlyAndIdempotent(t *testing.T) {
	f := newFixture(t)
	r := f.env.Roots
	ctx := context.Background()
	counts, err := RewriteDB(ctx, r.SourceDB(), r.Source, r.Target)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, u := range counts {
		got[u.Table+"."+u.Column] = u.Rows
	}
	if got["sessions.workspace_path"] != 4 || got["sessions.artifact_dir"] != 2 || got["report_outputs.reference"] != 1 {
		t.Fatalf("unexpected counts: %v", got)
	}
	db := r.SourceDB()
	if v := f.query1(t, db, `SELECT workspace_path FROM sessions WHERE id='proj-1'`); v != strings.Replace(f.wts[0], r.Source, r.Target, 1) {
		t.Errorf("workspace_path = %s", v)
	}
	if v := f.query1(t, db, `SELECT workspace_path FROM sessions WHERE id='proj-5'`); v != "/elsewhere/not/moved" {
		t.Errorf("foreign path rewritten: %s", v)
	}
	// A sibling directory that merely shares the prefix text must not change.
	if v := f.query1(t, db, `SELECT reference FROM report_outputs WHERE position=2`); v != r.Source+"2/data/lookalike" {
		t.Errorf("lookalike rewritten: %s", v)
	}
	if v := f.query1(t, db, `SELECT reference FROM report_outputs WHERE position=1`); !strings.HasPrefix(v, "https://") {
		t.Errorf("URL reference changed: %s", v)
	}
	if v := f.query1(t, db, `SELECT prompt FROM sessions WHERE id='proj-1'`); !strings.Contains(v, r.Source) {
		t.Errorf("history prompt was rewritten: %s", v)
	}
	again, err := RewriteDB(ctx, db, r.Source, r.Target)
	if err != nil || again != nil {
		t.Fatalf("second run must be a no-op, got %v, %v", again, err)
	}
}

// TestNoStrayOldPrefix scans every TEXT column of the real schema after the
// rewrite and fails on any old-prefix value outside the history allowlist.
func TestNoStrayOldPrefix(t *testing.T) {
	f := newFixture(t)
	r := f.env.Roots
	if _, err := RewriteDB(context.Background(), r.SourceDB(), r.Source, r.Target); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dsn(r.SourceDB(), ""))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	allowed := map[ColumnRef]bool{}
	for _, c := range HistoryColumns {
		allowed[c] = true
	}
	for _, c := range textColumns(t, db) {
		var n int
		q := `SELECT count(*) FROM ` + quote(c.Table) + ` WHERE substr(` + quote(c.Column) + `, 1, ?) = ?`
		if err := db.QueryRow(q, len(r.Source), r.Source).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 && !allowed[c] {
			// The lookalike "<root>2/..." row is the only legitimate non-history hit.
			var hits int
			q2 := `SELECT count(*) FROM ` + quote(c.Table) + ` WHERE (` + quote(c.Column) + ` = ? OR substr(` + quote(c.Column) + `, 1, ?) = ?)`
			if err := db.QueryRow(q2, r.Source, len(r.Source)+1, r.Source+"/").Scan(&hits); err != nil {
				t.Fatal(err)
			}
			if hits > 0 {
				t.Errorf("%s.%s still holds %d old-prefix values", c.Table, c.Column, hits)
			}
		}
	}
}

func textColumns(t *testing.T, db *sql.DB) []ColumnRef {
	t.Helper()
	rows, err := db.Query(`SELECT m.name, p.name FROM sqlite_master m, pragma_table_info(m.name) p
WHERE m.type='table' AND m.name NOT LIKE 'sqlite_%' AND (p.type LIKE '%TEXT%' OR p.type LIKE '%CHAR%' OR p.type = '' OR p.type LIKE '%JSON%')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []ColumnRef
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var c ColumnRef
		if err := rows.Scan(&c.Table, &c.Column); err != nil {
			t.Fatal(err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestPathNamedColumnsAreClassified fails when the schema gains a path-looking
// TEXT column that is neither rewritten nor explicitly exempted.
func TestPathNamedColumnsAreClassified(t *testing.T) {
	f := newFixture(t)
	db, err := sql.Open("sqlite", dsn(f.env.SourceDB(), ""))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	known := map[ColumnRef]bool{}
	for _, c := range append(append([]ColumnRef{}, PathColumns...), NotRewritten...) {
		known[c] = true
	}
	for _, c := range textColumns(t, db) {
		n := c.Column
		if (strings.Contains(n, "path") || strings.Contains(n, "dir") || strings.Contains(n, "cwd") ||
			strings.Contains(n, "root") || strings.HasSuffix(n, "file") || n == "reference" || strings.HasSuffix(n, "url")) && !known[c] {
			t.Errorf("%s.%s looks like a path/URL column but is in neither PathColumns nor NotRewritten", c.Table, c.Column)
		}
	}
	for _, c := range PathColumns {
		ok, err := columnExists(context.Background(), db, c)
		if err != nil || !ok {
			t.Errorf("PathColumns lists %s.%s, which is not in the schema (%v)", c.Table, c.Column, err)
		}
	}
}
