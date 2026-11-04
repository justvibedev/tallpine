package migratevo

import (
	"context"
	"database/sql"
)

// claudeConfigDirs lists the Claude config dirs a session's transcripts can
// live in: the default (~/.claude, returned as "") plus any account config dir
// recorded for the session (accounts run with CLAUDE_CONFIG_DIR set).
func (e *Env) claudeConfigDirs(ctx context.Context, db *sql.DB, sessionID string) ([]string, error) {
	out := []string{""}
	ok, err := columnExists(ctx, db, ColumnRef{"agent_native_sessions", "config_dir"})
	if err != nil || !ok {
		return out, err
	}
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT config_dir FROM agent_native_sessions
WHERE ao_session_id = ? AND harness = 'claude-code' AND config_dir <> '' ORDER BY config_dir`, sessionID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
