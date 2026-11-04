package sqlite

import (
	"database/sql"
)

// prepareReviewPerHarnessMigration makes the fresh 0080 rebuild executable on
// field databases that already recorded 0048/0049 as applied without actually
// running their SQL. Once 0080 has applied, it still repairs the physical review
// table if it is missing columns or constraints expected by current queries, but
// does not recreate review_session after the rebuild drops it.
func prepareReviewPerHarnessMigration(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var gooseTable int
	if err := tx.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'`,
	).Scan(&gooseTable); err != nil {
		return err
	}
	if gooseTable == 0 {
		return tx.Commit()
	}
	var applied80 int
	if err := tx.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = 80 ORDER BY id DESC LIMIT 1
), 0)`).Scan(&applied80); err != nil {
		return err
	}
	var applied48 int
	if err := tx.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = 48 ORDER BY id DESC LIMIT 1
), 0)`).Scan(&applied48); err != nil {
		return err
	}
	if applied48 == 0 {
		return tx.Commit()
	}
	var reviewTable int
	if err := tx.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'review'`,
	).Scan(&reviewTable); err != nil {
		return err
	}
	if reviewTable == 0 {
		return tx.Commit()
	}
	var agentSessionColumn int
	if err := tx.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('review') WHERE name = 'agent_session_id'`,
	).Scan(&agentSessionColumn); err != nil {
		return err
	}
	if agentSessionColumn == 0 {
		if _, err := tx.Exec(`ALTER TABLE review ADD COLUMN agent_session_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	if applied80 != 0 {
		if err := tx.Commit(); err != nil {
			return err
		}
		return repairReviewPerHarnessShape(db)
	}
	var reviewSessionTable int
	if err := tx.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'review_session'`,
	).Scan(&reviewSessionTable); err != nil {
		return err
	}
	if reviewSessionTable == 0 {
		if _, err := tx.Exec(`
CREATE TABLE review_session (
    session_id         TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    project_id         TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    harness            TEXT NOT NULL,
    reviewer_handle_id TEXT NOT NULL DEFAULT '',
    agent_session_id   TEXT NOT NULL DEFAULT '',
    created_at         TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at         TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (session_id, harness)
)`); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func repairReviewPerHarnessShape(db *sql.DB) error {
	ok, err := reviewHasSessionHarnessUnique(db)
	if err != nil || ok {
		return err
	}
	if _, err := db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		return err
	}
	defer func() { _, _ = db.Exec(`PRAGMA foreign_keys=ON`) }()

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`
CREATE TABLE review_repair (
    id                 TEXT PRIMARY KEY,
    session_id         TEXT NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    project_id         TEXT NOT NULL REFERENCES projects (id),
    harness            TEXT NOT NULL,
    pr_url             TEXT NOT NULL DEFAULT '',
    reviewer_handle_id TEXT NOT NULL DEFAULT '',
    agent_session_id   TEXT NOT NULL DEFAULT '',
    created_at         TIMESTAMP NOT NULL,
    updated_at         TIMESTAMP NOT NULL,
    UNIQUE(session_id, harness)
)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`
INSERT INTO review_repair (
    id, session_id, project_id, harness, pr_url, reviewer_handle_id,
    agent_session_id, created_at, updated_at
)
SELECT
    id, session_id, project_id, harness, pr_url, reviewer_handle_id,
    agent_session_id, created_at, updated_at
FROM review`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DROP TABLE review`); err != nil {
		return err
	}
	if _, err := tx.Exec(`ALTER TABLE review_repair RENAME TO review`); err != nil {
		return err
	}
	return tx.Commit()
}

func reviewHasSessionHarnessUnique(db *sql.DB) (bool, error) {
	rows, err := db.Query(`PRAGMA index_list('review')`)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	uniqueIndexes := []string{}
	for rows.Next() {
		var seq int
		var name, origin string
		var unique, partial int
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			return false, err
		}
		if unique == 0 {
			continue
		}
		uniqueIndexes = append(uniqueIndexes, name)
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	for _, name := range uniqueIndexes {
		if indexColumnsMatch(db, name, []string{"session_id", "harness"}) {
			return true, nil
		}
	}
	return false, nil
}

func indexColumnsMatch(db *sql.DB, indexName string, want []string) bool {
	rows, err := db.Query(`PRAGMA index_info(` + quoteSQLiteIdent(indexName) + `)`)
	if err != nil {
		return false
	}
	defer func() { _ = rows.Close() }()
	got := make([]string, 0, len(want))
	for rows.Next() {
		var seqno, cid int
		var name string
		if err := rows.Scan(&seqno, &cid, &name); err != nil {
			return false
		}
		got = append(got, name)
	}
	if rows.Err() != nil || len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
