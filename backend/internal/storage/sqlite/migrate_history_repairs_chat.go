package sqlite

import (
	"database/sql"
)

// providerSwitchingVersion is the provider switching migration's version.
const providerSwitchingVersion = 20261020131539535

// repairRenumberedProviderSwitchingMigrationHistory moves a database that ran
// provider switching under an earlier number in this branch's preview builds
// to its version. It would otherwise apply the migration again,
// and those numbers are left for main. The migration's table tells this
// branch's entry apart from another migration that took the number.
func repairRenumberedProviderSwitchingMigrationHistory(db *sql.DB) error {
	var gooseTable, turnsTable, applied int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'`).Scan(&gooseTable); err != nil || gooseTable == 0 {
		return err
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'session_terminal_turns'`).Scan(&turnsTable); err != nil || turnsTable == 0 {
		return err
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM goose_db_version WHERE version_id = ? AND is_applied = 1`, providerSwitchingVersion).Scan(&applied); err != nil || applied != 0 {
		return err
	}
	// Earlier builds also used 0196, 0199 and 0200, which main has since
	// taken. Those count as this migration's only while main's own schema for
	// that number is absent; goose then applies main's file, as it is missing.
	for _, old := range []struct {
		version int64
		mainHas string
	}{
		{version: 201}, {version: 216},
		{196, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'session_artifact_versions'`},
		{199, `SELECT COUNT(*) FROM pragma_table_info('conversation_messages') WHERE name = 'continuation'`},
		{200, `SELECT COUNT(*) FROM pragma_table_info('session_artifact_versions') WHERE name = 'source'`},
	} {
		if old.mainHas != "" {
			var present int
			if err := db.QueryRow(old.mainHas).Scan(&present); err != nil {
				return err
			}
			if present != 0 {
				continue
			}
		}
		res, err := db.Exec(`UPDATE goose_db_version SET version_id = ? WHERE version_id = ? AND is_applied = 1`, providerSwitchingVersion, old.version)
		if err != nil {
			return err
		}
		if moved, err := res.RowsAffected(); err != nil || moved > 0 {
			return err
		}
	}
	return nil
}

// repairRenumberedChatMigrationHistory preserves databases opened by this
// feature branch before its Chat migrations moved from 0052-0065 to 0066-0079.
// The files are byte-for-byte identical after the rename, so recording the new
// numbers is safer than replaying their ALTER/CREATE statements over an already
// upgraded schema. Version 0052 now belongs to model usage on main; if that
// physical schema is absent, release the burned ledger entry so goose can apply
// the real 0052 migration below.
func repairRenumberedChatMigrationHistory(db *sql.DB) error {
	// Probe on a read-only query first. A fresh database (the common import
	// path and the cli import test) has no goose_db_version table yet, so there
	// is nothing to repair; entering a write transaction would needlessly
	// create WAL/SHM files and is the exact I/O surface that surfaced as a
	// transient SQLITE_IOERR_WRITE on macOS CI runners.
	var gooseTable int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'`,
	).Scan(&gooseTable); err != nil {
		return err
	}
	if gooseTable == 0 {
		return nil
	}

	var chatColumn, conversationsTable int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name = 'session_mode'`,
	).Scan(&chatColumn); err != nil {
		return err
	}
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'conversations'`,
	).Scan(&conversationsTable); err != nil {
		return err
	}
	if chatColumn == 0 || conversationsTable == 0 {
		return nil
	}

	// Only enter a write transaction when there is actual repair work to do.
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Earlier dev builds used 0179-0181 for hibernation. Move that physical
	// schema to 0190 so main's sender metadata, startup steps, and artifacts apply.
	if _, err := tx.Exec(`
UPDATE goose_db_version SET version_id = 190
WHERE is_applied = 1
  AND EXISTS (SELECT 1 FROM pragma_table_info('sessions') WHERE name = 'hibernated_at')
  AND ((version_id = 179 AND NOT EXISTS (SELECT 1 FROM pragma_table_info('conversation_messages') WHERE name = 'sender_session_id'))
    OR (version_id = 180 AND NOT EXISTS (SELECT 1 FROM pragma_table_info('sessions') WHERE name = 'provision_steps'))
    OR (version_id = 181 AND NOT EXISTS (SELECT 1 FROM pragma_table_info('sessions') WHERE name IN ('artifact_dir', 'startup_cue_json'))))
  AND NOT EXISTS (SELECT 1 FROM goose_db_version WHERE version_id = 190)`); err != nil {
		return err
	}

	legacyApplied := false
	for oldVersion := int64(52); oldVersion <= 65; oldVersion++ {
		var applied int
		if err := tx.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = ? ORDER BY id DESC LIMIT 1
), 0)`, oldVersion).Scan(&applied); err != nil {
			return err
		}
		if applied == 0 {
			continue
		}
		legacyApplied = true
		newVersion := oldVersion + 14
		var alreadyMapped int
		if err := tx.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = ? ORDER BY id DESC LIMIT 1
), 0)`, newVersion).Scan(&alreadyMapped); err != nil {
			return err
		}
		if alreadyMapped == 0 {
			if _, err := tx.Exec(
				`INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, 1)`,
				newVersion,
			); err != nil {
				return err
			}
		}
	}
	if !legacyApplied {
		return tx.Commit()
	}

	var modelUsageTable int
	if err := tx.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'model_usage_events'`,
	).Scan(&modelUsageTable); err != nil {
		return err
	}
	if modelUsageTable == 0 {
		if _, err := tx.Exec(`DELETE FROM goose_db_version WHERE version_id = 52`); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// repairRenumberedUsageCostMigrationHistory preserves development databases
// opened while this feature's migrations occupied 0109-0112 and, later,
// 0110-0113. Main now owns 0109-0112, while the same usage migrations live at
// 0113-0116. Record each physically present usage effect at its canonical
// number, then release only the collided main numbers whose physical effects
// are absent.
func repairRenumberedUsageCostMigrationHistory(db *sql.DB) error {
	var gooseTable int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'`,
	).Scan(&gooseTable); err != nil {
		return err
	}
	if gooseTable == 0 {
		return nil
	}

	// provider_hint and the durable event pricing columns arrived together in
	// the first usage-cost migration. Requiring the complete retained shape
	// avoids rewriting unrelated migration history on databases without it.
	var costShape int
	if err := db.QueryRow(`
SELECT (SELECT COUNT(*) FROM pragma_table_info('usage_bindings') WHERE name = 'provider_hint')
     + (SELECT COUNT(*) FROM pragma_table_info('model_usage_events') WHERE name = 'billing_provider_id')
     + (SELECT COUNT(*) FROM pragma_table_info('model_usage_events') WHERE name = 'estimated_cost_nanos')
     + (SELECT COUNT(*) FROM pragma_table_info('model_usage_events') WHERE name = 'pricing_version')`).Scan(&costShape); err != nil {
		return err
	}
	if costShape != 4 {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	markApplied := func(version int64) error {
		var applied int
		if err := tx.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = ? ORDER BY id DESC LIMIT 1
), 0)`, version).Scan(&applied); err != nil {
			return err
		}
		if applied != 0 {
			return nil
		}
		_, err := tx.Exec(
			`INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, 1)`,
			version,
		)
		return err
	}

	if err := markApplied(113); err != nil {
		return err
	}
	var canonicalIndex int
	if err := tx.QueryRow(`
SELECT COUNT(*) FROM sqlite_master
WHERE type = 'index' AND name = 'idx_model_usage_events_canonical_cost_candidates'`).Scan(&canonicalIndex); err != nil {
		return err
	}
	if canonicalIndex != 0 {
		if err := markApplied(114); err != nil {
			return err
		}
	}

	var measurementShape int
	if err := tx.QueryRow(`
SELECT COUNT(*) FROM pragma_table_info('model_usage_events')
WHERE name IN ('usage_measurement_kind', 'provider_usage_json')`).Scan(&measurementShape); err != nil {
		return err
	}
	if measurementShape == 2 {
		if err := markApplied(115); err != nil {
			return err
		}
	}

	var providerSource int
	if err := tx.QueryRow(`
SELECT COUNT(*) FROM pragma_table_info('model_usage_events')
WHERE name = 'billing_provider_source'`).Scan(&providerSource); err != nil {
		return err
	}
	if providerSource != 0 {
		if err := markApplied(116); err != nil {
			return err
		}
	}

	var latestPromptShape, branchShape, prTriggerShape, cloudShape int
	if err := tx.QueryRow(`
SELECT COUNT(*) FROM pragma_table_info('sessions')
WHERE name = 'latest_user_prompt_at'`).Scan(&latestPromptShape); err != nil {
		return err
	}
	if err := tx.QueryRow(`
SELECT COUNT(*) FROM pragma_table_info('conversation_branches')
WHERE name IN ('strategy', 'replay_cutoff_sequence', 'replay_truncated', 'provider_scope_id')`).Scan(&branchShape); err != nil {
		return err
	}
	if err := tx.QueryRow(`
SELECT COUNT(*) FROM sqlite_master
WHERE type = 'trigger' AND name = 'pr_cdc_update'
  AND instr(COALESCE(sql, ''), 'auto_inject_ci') > 0`).Scan(&prTriggerShape); err != nil {
		return err
	}
	if err := tx.QueryRow(`
SELECT COUNT(*) FROM pragma_table_info('app_settings')
WHERE name = 'cloud_offering'`).Scan(&cloudShape); err != nil {
		return err
	}
	for _, migration := range []struct {
		version int64
		present bool
	}{
		{version: 109, present: latestPromptShape != 0},
		{version: 110, present: branchShape == 4},
		{version: 111, present: prTriggerShape != 0},
		{version: 112, present: cloudShape != 0},
	} {
		if migration.present {
			continue
		}
		if _, err := tx.Exec(
			`DELETE FROM goose_db_version WHERE version_id = ?`, migration.version,
		); err != nil {
			return err
		}
	}

	return tx.Commit()
}
