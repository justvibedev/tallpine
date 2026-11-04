package sqlite

import (
	"database/sql"
)

// repairRenumberedCodexProfileMigrationHistory preserves development
// databases opened before the stacked Codex-profile migrations were moved out
// of version numbers subsequently claimed by main. Early Phase 1 builds used
// 0117 to drop agent_inventory_cache; 0117 now enables Kimi usage. Release the
// collided ledger entry only when the canonical physical effect is absent so
// goose can apply the real migration. The drop remains safe at canonical 0122.
func repairRenumberedCodexProfileMigrationHistory(db *sql.DB) error {
	var gooseTable int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'`,
	).Scan(&gooseTable); err != nil {
		return err
	}
	if gooseTable == 0 {
		return nil
	}

	var applied117 int
	if err := db.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = 117 ORDER BY id DESC LIMIT 1
), 0)`).Scan(&applied117); err != nil {
		return err
	}
	if applied117 == 0 {
		return nil
	}

	var kimiUsageShape int
	if err := db.QueryRow(`
SELECT (SELECT COUNT(*) FROM sqlite_master
        WHERE type = 'table' AND name = 'usage_bindings'
          AND instr(COALESCE(sql, ''), '''kimi''') > 0)
     + (SELECT COUNT(*) FROM sqlite_master
        WHERE type = 'table' AND name = 'usage_sources'
          AND instr(COALESCE(sql, ''), '''kimi_wire''') > 0)`).Scan(&kimiUsageShape); err != nil {
		return err
	}
	if kimiUsageShape == 2 {
		return nil
	}

	_, err := db.Exec(`DELETE FROM goose_db_version WHERE version_id = 117`)
	return err
}

// repairRenumberedAgentInventoryMigrationHistory preserves development
// databases opened while this branch used 0119, 0120, or 0121 to drop
// agent_inventory_cache. Main now owns those versions for completed-plan
// finalization, activity timestamp normalization, and reviewer configuration;
// the identical cache drop lives at 0122. An absent cache table plus a collided
// ledger entry identifies the branch history: mark the physical drop at 0122,
// then release the newest collided version so goose can apply main's migration
// with WithAllowMissing.
func repairRenumberedAgentInventoryMigrationHistory(db *sql.DB) error {
	var gooseTable int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'`,
	).Scan(&gooseTable); err != nil {
		return err
	}
	if gooseTable == 0 {
		return nil
	}

	var applied119 int
	if err := db.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = 119 ORDER BY id DESC LIMIT 1
), 0)`).Scan(&applied119); err != nil {
		return err
	}
	var applied120 int
	if err := db.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = 120 ORDER BY id DESC LIMIT 1
), 0)`).Scan(&applied120); err != nil {
		return err
	}
	var applied121 int
	if err := db.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = 121 ORDER BY id DESC LIMIT 1
), 0)`).Scan(&applied121); err != nil {
		return err
	}
	var applied122 int
	if err := db.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = 122 ORDER BY id DESC LIMIT 1
), 0)`).Scan(&applied122); err != nil {
		return err
	}
	if applied122 != 0 || (applied119 == 0 && applied120 == 0 && applied121 == 0) {
		return nil
	}

	var inventoryTable int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'agent_inventory_cache'`,
	).Scan(&inventoryTable); err != nil {
		return err
	}
	if inventoryTable != 0 {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (122, 1)`); err != nil {
		return err
	}
	collidedVersion := int64(119)
	if applied120 != 0 {
		collidedVersion = 120
	}
	if applied121 != 0 {
		collidedVersion = 121
	}
	if _, err := tx.Exec(`DELETE FROM goose_db_version WHERE version_id = ?`, collidedVersion); err != nil {
		return err
	}
	return tx.Commit()
}

// repairRenumberedPRReviewPartialMigrationHistory preserves databases opened by
// earlier revisions of this branch: the review_partial column first shipped as
// 0123, a number main later claimed for agent_install_jobs. On those databases
// goose would skip main's 0123 (its effects missing) and fail 0130 on the
// duplicate column. Remap the recorded history — review_partial physically
// present while agent_install_jobs is not identifies the branch build — and
// re-initialize certainty conservatively, matching the migration default: rows
// written before the completeness semantics landed carry no reliable signal, so
// they stay uncertain until the next successful full review fetch.
func repairRenumberedPRReviewPartialMigrationHistory(db *sql.DB) error {
	var gooseTable int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'`,
	).Scan(&gooseTable); err != nil {
		return err
	}
	if gooseTable == 0 {
		return nil
	}

	var reviewPartialColumn int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('pr') WHERE name = 'review_partial'`,
	).Scan(&reviewPartialColumn); err != nil {
		return err
	}
	if reviewPartialColumn == 0 {
		return nil
	}

	// The branch's 0123 ran only on builds predating main's 0123-0129. If
	// agent_install_jobs exists, main's 0123 already ran and this database took
	// the migration through 0130 (or never saw the old numbering) — nothing to
	// remap.
	var agentInstallJobsTable int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'agent_install_jobs'`,
	).Scan(&agentInstallJobsTable); err != nil {
		return err
	}
	if agentInstallJobsTable != 0 {
		return nil
	}

	var applied123 int
	if err := db.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = 123 ORDER BY id DESC LIMIT 1
), 0)`).Scan(&applied123); err != nil {
		return err
	}
	if applied123 == 0 {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Release 123 so goose applies main's agent_install_jobs, and record 130 as
	// applied so goose does not replay the ALTER on the existing column.
	if _, err := tx.Exec(`DELETE FROM goose_db_version WHERE version_id = 123`); err != nil {
		return err
	}
	var applied130 int
	if err := tx.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = 130 ORDER BY id DESC LIMIT 1
), 0)`).Scan(&applied130); err != nil {
		return err
	}
	if applied130 == 0 {
		if _, err := tx.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (130, 1)`); err != nil {
			return err
		}
	}
	// The column predates the conservative default; re-initialize to uncertain
	// so pre-semantics rows cannot publish exact thread counts.
	if _, err := tx.Exec(`UPDATE pr SET review_partial = TRUE`); err != nil {
		return err
	}
	return tx.Commit()
}

// repairBurnedAutomationsMigrationHistory preserves development databases that
// applied automations at an earlier version, including 159, which now belongs
// to main's PR discussion migration. The physical schemas determine whether
// Goose should apply PR discussion at 159 and automations at canonical 161.
// An older automation build also used 156; release it only when the canonical
// session-provisioning columns are absent. Once 162 has dropped the PR
// discussion columns, their absence no longer says anything about 159.
func repairBurnedAutomationsMigrationHistory(db *sql.DB) error {
	var gooseTable int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'`,
	).Scan(&gooseTable); err != nil {
		return err
	}
	if gooseTable == 0 {
		return nil
	}

	var automationsTable, automationRunID, provisionColumns, discussionColumn int
	if err := db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'automations'),
		(SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name = 'automation_run_id'),
		(SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name IN ('provision_state', 'provision_error')),
		(SELECT COUNT(*) FROM pragma_table_info('pr') WHERE name = 'discussion_comment_count')`).Scan(
		&automationsTable, &automationRunID, &provisionColumns, &discussionColumn,
	); err != nil {
		return err
	}
	applied := func(version int64) (bool, error) {
		var value int
		if err := db.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = ? ORDER BY id DESC LIMIT 1
), 0)`, version).Scan(&value); err != nil {
			return false, err
		}
		return value == 1, nil
	}
	applied159, err := applied(159)
	if err != nil {
		return err
	}
	applied161, err := applied(161)
	if err != nil {
		return err
	}
	applied156, err := applied(156)
	if err != nil {
		return err
	}
	applied162, err := applied(162)
	if err != nil {
		return err
	}
	if automationsTable > 0 && automationRunID > 0 {
		if applied156 && provisionColumns != 2 {
			if _, err := db.Exec(`DELETE FROM goose_db_version WHERE version_id = 156`); err != nil {
				return err
			}
		}
		if !applied161 {
			if _, err := db.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (161, 1)`); err != nil {
				return err
			}
		}
	} else if applied161 {
		if _, err := db.Exec(`DELETE FROM goose_db_version WHERE version_id = 161`); err != nil {
			return err
		}
	}
	if applied162 {
		return nil
	}
	if discussionColumn > 0 && !applied159 {
		_, err = db.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (159, 1)`)
		return err
	}
	if discussionColumn == 0 && applied159 {
		_, err = db.Exec(`DELETE FROM goose_db_version WHERE version_id = 159`)
		return err
	}
	return nil
}
