package sqlite

import (
	"database/sql"
	"strings"
)

// prepareCancelledConversationTurnsMigration repairs the staging table left
// by an interrupted 0118 table rebuild. The migration runs without a wrapping
// transaction because it temporarily disables foreign keys, so a process exit
// can leave conversation_turns_next behind and make every retry fail at CREATE
// TABLE. When the source table remains it is still authoritative and the stale
// copy is discarded. If the interruption happened after the source was
// dropped, promote the staging table back to the source name and let 0118
// perform the complete rebuild again.
func prepareCancelledConversationTurnsMigration(db *sql.DB) error {
	var sourceTable, stagingTable int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'conversation_turns'`,
	).Scan(&sourceTable); err != nil {
		return err
	}
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'conversation_turns_next'`,
	).Scan(&stagingTable); err != nil {
		return err
	}
	if stagingTable == 0 {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if sourceTable != 0 {
		if _, err := tx.Exec(`DROP TABLE conversation_turns_next`); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(`ALTER TABLE conversation_turns_next RENAME TO conversation_turns`); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// prepareUsageCostMigration releases a falsely-applied usage migration before
// 0101 adds columns to its tables. Some field profiles recorded versions
// 44-53 from a foreign build without ever creating the real 0052 usage schema.
func prepareUsageCostMigration(db *sql.DB) error {
	var gooseTable int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'`,
	).Scan(&gooseTable); err != nil || gooseTable == 0 {
		return err
	}
	var usageTables int
	if err := db.QueryRow(`
SELECT COUNT(*) FROM sqlite_master
WHERE type = 'table' AND name IN ('usage_bindings', 'usage_sources', 'model_usage_events')`).Scan(&usageTables); err != nil {
		return err
	}
	if usageTables == 3 {
		return nil
	}
	_, err := db.Exec(`DELETE FROM goose_db_version WHERE version_id = 52`)
	return err
}

// prepareAutoInjectReviewMigration preserves development databases whose
// physical review-injection schema exists without the corresponding goose
// ledger entry. Without this repair, goose replays 0084 and startup stops on
// the first duplicate column. Fresh schemas still run the migration normally;
// only the complete four-table shape is accepted as already applied.
func prepareAutoInjectReviewMigration(db *sql.DB) error {
	var gooseTable int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'`,
	).Scan(&gooseTable); err != nil {
		return err
	}
	if gooseTable == 0 {
		return nil
	}

	var applied int
	if err := db.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = 84 ORDER BY id DESC LIMIT 1
), 0)`).Scan(&applied); err != nil {
		return err
	}
	if applied != 0 {
		return nil
	}

	for _, table := range []string{"sessions", "review_run", "pr_reviews", "pr_comment"} {
		var present int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = 'auto_inject_review'`, table,
		).Scan(&present); err != nil {
			return err
		}
		if present == 0 {
			return nil
		}
	}

	_, err := db.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (84, 1)`)
	return err
}

// preparePRCommentReviewIDMigration preserves development databases that
// acquired pr_comment.review_id while this migration was being renumbered but
// do not have its canonical 0106 ledger entry. Recording the physically present
// effect prevents goose from replaying the ALTER TABLE over the existing column.
func preparePRCommentReviewIDMigration(db *sql.DB) error {
	var gooseTable int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'`,
	).Scan(&gooseTable); err != nil {
		return err
	}
	if gooseTable == 0 {
		return nil
	}

	var applied, reviewIDColumn int
	if err := db.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = 106 ORDER BY id DESC LIMIT 1
), 0)`).Scan(&applied); err != nil {
		return err
	}
	if applied != 0 {
		return nil
	}
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('pr_comment') WHERE name = 'review_id'`,
	).Scan(&reviewIDColumn); err != nil {
		return err
	}
	if reviewIDColumn == 0 {
		return nil
	}
	_, err := db.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (106, 1)`)
	return err
}

// prepareBrowserVerifierMigration preserves development databases that ran an
// earlier version of this branch where the verifier was mistakenly added to
// shipped migration 0048. The restored 0048 no longer owns the column, so mark
// the new 0081 migration applied when its exact schema effect already exists;
// otherwise goose applies 0081 normally.
func prepareBrowserVerifierMigration(db *sql.DB) error {
	var gooseTable int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'`,
	).Scan(&gooseTable); err != nil {
		return err
	}
	if gooseTable == 0 {
		return nil
	}
	var applied int
	if err := db.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = 81 ORDER BY id DESC LIMIT 1
), 0)`).Scan(&applied); err != nil {
		return err
	}
	if applied != 0 {
		return nil
	}
	var verifierColumn int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name = 'browser_capability_verifier'`,
	).Scan(&verifierColumn); err != nil {
		return err
	}
	if verifierColumn == 0 {
		return nil
	}
	_, err := db.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (81, 1)`)
	return err
}

// prepareQueuedTurnPromotionMigration preserves development databases that ran
// the promotion migration while it still used version 86 or 88. Upstream now
// owns both numbers, so record the promotion schema as version 89. If version 88
// came from the old promotion migration, remove that ledger entry so goose can
// apply upstream's auto-inject-CI migration at its canonical version.
// prepareSessionReviewerAgentConfigMigration preserves databases that
// already have sessions.reviewer_agent_config from an in-flight local run but
// do not yet record the canonical 0118 migration version. Recording that
// physically present effect prevents goose from replaying the ALTER TABLE over
// the existing column on the next startup.
func prepareSessionReviewerAgentConfigMigration(db *sql.DB) error {
	var gooseTable int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'`,
	).Scan(&gooseTable); err != nil {
		return err
	}
	if gooseTable == 0 {
		return nil
	}

	var applied, reviewerConfigColumn int
	if err := db.QueryRow(`
SELECT
    COALESCE((
        SELECT is_applied FROM goose_db_version
        WHERE version_id = 118 ORDER BY id DESC LIMIT 1
    ), 0),
    (SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name = 'reviewer_agent_config')
`).Scan(&applied, &reviewerConfigColumn); err != nil {
		return err
	}
	if applied != 0 || reviewerConfigColumn == 0 {
		return nil
	}

	_, err := db.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (118, 1)`)
	return err
}

func prepareQueuedTurnPromotionMigration(db *sql.DB) error {
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

	var promotionColumns int
	if err := tx.QueryRow(`
SELECT COUNT(*) FROM pragma_table_info('conversation_turns')
WHERE name IN ('promotion_started_at', 'promoted_to_turn_id')`).Scan(&promotionColumns); err != nil {
		return err
	}
	if promotionColumns != 2 {
		return tx.Commit()
	}

	var applied89 int
	if err := tx.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = 89 ORDER BY id DESC LIMIT 1
), 0)`).Scan(&applied89); err != nil {
		return err
	}
	if applied89 == 0 {
		if _, err := tx.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (89, 1)`); err != nil {
			return err
		}
	}

	var applied88, autoInjectCIColumns int
	if err := tx.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = 88 ORDER BY id DESC LIMIT 1
), 0)`).Scan(&applied88); err != nil {
		return err
	}
	if applied88 != 0 {
		if err := tx.QueryRow(`
SELECT (SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name = 'auto_inject_ci')
     + (SELECT COUNT(*) FROM pragma_table_info('pr') WHERE name = 'auto_inject_ci')`).Scan(&autoInjectCIColumns); err != nil {
			return err
		}
		if autoInjectCIColumns == 0 {
			if _, err := tx.Exec(`DELETE FROM goose_db_version WHERE version_id = 88`); err != nil {
				return err
			}
		}
	}

	return tx.Commit()
}

func prepareBurnedSchemaRepairs(db *sql.DB) error {
	var gooseTable int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'`,
	).Scan(&gooseTable); err != nil {
		return err
	}
	if gooseTable == 0 {
		return nil
	}
	for _, rc := range schemaRepairs {
		var applied int
		if err := db.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = ? ORDER BY id DESC LIMIT 1
), 0)`, rc.version).Scan(&applied); err != nil {
			return err
		}
		if applied == 0 {
			continue
		}
		var tableCount int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, rc.table,
		).Scan(&tableCount); err != nil {
			return err
		}
		if tableCount == 0 {
			continue
		}
		var columnCount int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, rc.table, rc.column,
		).Scan(&columnCount); err != nil {
			return err
		}
		if columnCount > 0 {
			continue
		}
		if _, err := db.Exec(rc.addDDL); err != nil {
			return err
		}
		for _, stmt := range rc.postAdd {
			if _, err := db.Exec(stmt); err != nil {
				return err
			}
		}
	}
	return nil
}

func quoteSQLiteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
