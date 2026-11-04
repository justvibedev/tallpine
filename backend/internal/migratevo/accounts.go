package migratevo

import (
	"context"
	"database/sql"
)

// listAccounts returns agent_accounts rows whose home is under the source root.
func (e *Env) listAccounts(ctx context.Context, db *sql.DB) ([]AccountNote, error) {
	ok, err := columnExists(ctx, db, ColumnRef{"agent_accounts", "home"})
	if err != nil || !ok {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT id, provider, home FROM agent_accounts ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []AccountNote
	for rows.Next() {
		var a AccountNote
		if err := rows.Scan(&a.ID, &a.Provider, &a.Home); err != nil {
			return nil, err
		}
		if _, moved := e.MapPath(a.Home); moved {
			out = append(out, a)
		}
	}
	return out, rows.Err()
}
