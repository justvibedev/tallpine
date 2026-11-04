package migratevo

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

const markerTable = "migration_markers"
const markerName = "migration_vo_to_tallpine"

func dsn(path, extra string) string {
	u := url.URL{Scheme: "file", Path: path}
	return u.String() + "?_pragma=busy_timeout(5000)" + extra
}

func quote(id string) string { return `"` + strings.ReplaceAll(id, `"`, `""`) + `"` }

func columnExists(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, c ColumnRef) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info(?) WHERE name = ?`, c.Table, c.Column).Scan(&n)
	return n > 0, err
}

// prefixWhere matches values equal to the source root or under it. The length
// is in characters, which is what SQLite's substr/length count.
func prefixWhere(col string) string {
	return fmt.Sprintf(`substr(%[1]s, 1, ?1) = ?2 AND (length(%[1]s) = ?1 OR substr(%[1]s, ?1 + 1, 1) = '/')`, quote(col))
}

// CountPathRows returns, per PathColumn present in db, how many rows a rewrite
// would change. It only reads.
func CountPathRows(ctx context.Context, db *sql.DB, source string) ([]DBUpdate, error) {
	var out []DBUpdate
	n := utf8.RuneCountInString(source)
	for _, c := range PathColumns {
		ok, err := columnExists(ctx, db, c)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		var rows int64
		//nolint:gosec // identifiers come from the PathColumns constant and are quoted
		q := fmt.Sprintf(`SELECT count(*) FROM %s WHERE %s`, quote(c.Table), prefixWhere(c.Column))
		if err := db.QueryRowContext(ctx, q, n, source).Scan(&rows); err != nil {
			return nil, fmt.Errorf("count %s.%s: %w", c.Table, c.Column, err)
		}
		out = append(out, DBUpdate{c.Table, c.Column, rows})
	}
	return out, nil
}

// RewriteDB rewrites every PathColumn from source to target in one transaction
// and records a marker row. A present marker makes it a no-op. It returns the
// rows changed per column.
func RewriteDB(ctx context.Context, dbPath, source, target string) ([]DBUpdate, error) {
	db, err := sql.Open("sqlite", dsn(dbPath, ""))
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS `+markerTable+` (name TEXT PRIMARY KEY, done_at TEXT NOT NULL)`); err != nil {
		return nil, err
	}
	var seen int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM `+markerTable+` WHERE name = ?`, markerName).Scan(&seen); err != nil {
		return nil, err
	}
	if seen > 0 {
		return nil, nil
	}
	n := utf8.RuneCountInString(source)
	var out []DBUpdate
	for _, c := range PathColumns {
		ok, err := columnExists(ctx, tx, c)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		col := quote(c.Column)
		//nolint:gosec // identifiers come from the PathColumns constant and are quoted
		q := fmt.Sprintf(`UPDATE %s SET %s = ?3 || substr(%s, ?1 + 1) WHERE %s`, quote(c.Table), col, col, prefixWhere(c.Column))
		res, err := tx.ExecContext(ctx, q, n, source, target)
		if err != nil {
			return nil, fmt.Errorf("rewrite %s.%s: %w", c.Table, c.Column, err)
		}
		rows, _ := res.RowsAffected()
		out = append(out, DBUpdate{c.Table, c.Column, rows})
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO `+markerTable+` (name, done_at) VALUES (?, datetime('now'))`, markerName); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}
