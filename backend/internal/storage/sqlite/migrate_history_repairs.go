package sqlite

import (
	"database/sql"
	"fmt"
)

// migrationHistorySteps lists, in the order they must run, every repair that
// reconciles goose_db_version (or a half-applied table rebuild) before
// goose.Up. Each step is idempotent. The label prefixes the startup error.
var migrationHistorySteps = []struct {
	label string
	run   func(*sql.DB) error
}{
	{"repair renumbered agent-install-jobs migration history", repairRenumberedAgentInstallJobsMigrationHistory},
	{"repair renumbered provider-switching migration history", repairRenumberedProviderSwitchingMigrationHistory},
	{"repair renumbered chat migration history", repairRenumberedChatMigrationHistory},
	{"repair renumbered cue migration history", repairRenumberedCueMigrationHistory},
	{"repair renumbered task-provisioning migration history", repairRenumberedTaskProvisioningMigrationHistory},
	{"repair renumbered usage-cost migration history", repairRenumberedUsageCostMigrationHistory},
	{"repair renumbered usage-limit migration history", repairRenumberedUsageLimitMigrationHistory},
	{"prepare cancelled conversation-turn migration", prepareCancelledConversationTurnsMigration},
	{"repair renumbered Codex profile migration history", repairRenumberedCodexProfileMigrationHistory},
	{"repair renumbered agent-inventory migration history", repairRenumberedAgentInventoryMigrationHistory},
	{"prepare usage cost migration", prepareUsageCostMigration},
	{"prepare auto-inject-review migration", prepareAutoInjectReviewMigration},
	{"prepare PR comment review-id migration", preparePRCommentReviewIDMigration},
	{"repair renumbered PR review-partial migration history", repairRenumberedPRReviewPartialMigrationHistory},
	{"repair burned automations migration history", repairBurnedAutomationsMigrationHistory},
	{"prepare burned schema repairs", prepareBurnedSchemaRepairs},
	{"prepare review per-harness migration", prepareReviewPerHarnessMigration},
	{"prepare browser verifier migration", prepareBrowserVerifierMigration},
	{"prepare queued-turn promotion migration", prepareQueuedTurnPromotionMigration},
	{"prepare session reviewer agent-config migration", prepareSessionReviewerAgentConfigMigration},
}

// runMigrationHistorySteps applies migrationHistorySteps in order.
func runMigrationHistorySteps(db *sql.DB) error {
	for _, step := range migrationHistorySteps {
		if err := step.run(db); err != nil {
			return fmt.Errorf("%s: %w", step.label, err)
		}
	}
	return nil
}

const (
	usageLimitMigrationVersion      int64 = 20261019220421171
	usageLimitResetMigrationVersion int64 = 20261019220421514
)

// Adopt identifiable quota previews without skipping main's unrelated 0213/0214.
// Foreign 0201 receipts remain intact; collided quota receipts move with their schema.
func repairRenumberedUsageLimitMigrationHistory(db *sql.DB) error {
	var gooseTable int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'`).Scan(&gooseTable); err != nil {
		return err
	}
	if gooseTable == 0 {
		return nil
	}
	var legacyApplied, previewApplied, canonicalApplied int
	if err := db.QueryRow(`
SELECT COALESCE((SELECT is_applied FROM goose_db_version WHERE version_id = 201 ORDER BY id DESC LIMIT 1), 0),
       COALESCE((SELECT is_applied FROM goose_db_version WHERE version_id = 213 ORDER BY id DESC LIMIT 1), 0),
       COALESCE((SELECT is_applied FROM goose_db_version WHERE version_id = ? ORDER BY id DESC LIMIT 1), 0)`,
		usageLimitMigrationVersion).Scan(&legacyApplied, &previewApplied, &canonicalApplied); err != nil {
		return err
	}
	if legacyApplied != 1 && previewApplied != 1 {
		return nil
	}
	var quotaShape int
	if err := db.QueryRow(`
SELECT (SELECT COUNT(*) FROM pragma_table_info('conversation_turns')
        WHERE (name = 'usage_limit_resets_at' AND upper(type) = 'TIMESTAMP' AND "notnull" = 0)
           OR (name = 'usage_limit_auto_resume' AND upper(type) = 'INTEGER' AND "notnull" = 1 AND dflt_value = '0')
           OR (name = 'usage_limit_resume_error' AND upper(type) = 'TEXT' AND "notnull" = 1 AND dflt_value = quote('')))
     + (SELECT COUNT(*) FROM sqlite_master WHERE type = 'index'
        AND name = 'conversation_turns_auto_resume' AND tbl_name = 'conversation_turns'
        AND instr(sql, 'usage_limit_resets_at') > 0 AND instr(sql, 'usage_limit_auto_resume = 1') > 0)
     + (SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger'
        AND name = 'conversation_turns_auto_resume_cdc_update' AND tbl_name = 'conversation_turns'
        AND instr(sql, 'usage_limit_auto_resume') > 0 AND instr(sql, 'usage_limit_resume_error') > 0
        AND instr(sql, 'INSERT INTO change_log') > 0)`).Scan(&quotaShape); err != nil {
		return err
	}
	if quotaShape != 5 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// A quota-shaped preview with no stale-binary columns owns the old 0213
	// receipt. Relocate it so Goose can apply main's shipped 0213 normally.
	moved, err := tx.Exec(`UPDATE goose_db_version SET version_id = ? WHERE version_id = 213
AND NOT EXISTS (SELECT 1 FROM pragma_table_info('sessions') WHERE name = 'stale_binary_since')`, usageLimitMigrationVersion)
	if err != nil {
		return err
	}
	n, err := moved.RowsAffected()
	if err != nil {
		return err
	}
	if canonicalApplied != 1 && n == 0 {
		if _, err := tx.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, 1)`, usageLimitMigrationVersion); err != nil {
			return err
		}
	}
	// The deadline CDC body identifies quota's old 0214; main's receipt is
	// retained when its version-refresh column is present.
	if _, err := tx.Exec(`UPDATE goose_db_version SET version_id = ? WHERE version_id = 214
AND NOT EXISTS (SELECT 1 FROM pragma_table_info('app_settings') WHERE name = 'version_refresh')
AND EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'trigger'
 AND name = 'conversation_turns_auto_resume_cdc_update'
 AND instr(sql, 'OLD.usage_limit_resets_at IS NOT NEW.usage_limit_resets_at') > 0)`, usageLimitResetMigrationVersion); err != nil {
		return err
	}
	return tx.Commit()
}

// repairRenumberedCueMigrationHistory preserves preview Cue databases that
// recorded 0149, 0155, 0156, 0159, 0161, 0162, or 0163 for Cues before main
// assigned those versions to other features. Move only an identifiable Cue
// schema to 0168 before the upstream migration repairs inspect or reuse old
// entries. 0163 is now the fx harness migration, so a 0163 row is cues only
// when that harness is still absent.
func repairRenumberedCueMigrationHistory(db *sql.DB) error {
	var gooseTable int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'`).Scan(&gooseTable); err != nil || gooseTable == 0 {
		return err
	}
	var cueColumns, reviewerColumn, provisionColumns, unrealHarness int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('cues') WHERE name IN ('id', 'project_id', 'name', 'description', 'type', 'command', 'prompt', 'created_at', 'updated_at')`).Scan(&cueColumns); err != nil {
		return err
	}
	if cueColumns != 9 {
		return nil
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('review') WHERE name = 'interface_mode'`).Scan(&reviewerColumn); err != nil {
		return err
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name IN ('provision_state', 'provision_error')`).Scan(&provisionColumns); err != nil {
		return err
	}
	if err := db.QueryRow(`SELECT instr(sql, 'unreal-agent') FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`).Scan(&unrealHarness); err != nil {
		return err
	}
	var applied149, applied155, applied156, applied159, applied161, applied162, applied163, applied168, discussionCountColumn, fxHarness int
	for _, item := range []struct {
		version int
		result  *int
	}{{149, &applied149}, {155, &applied155}, {156, &applied156}, {159, &applied159}, {161, &applied161}, {162, &applied162}, {163, &applied163}, {168, &applied168}} {
		if err := db.QueryRow(`SELECT COALESCE((SELECT is_applied FROM goose_db_version WHERE version_id = ? ORDER BY id DESC LIMIT 1), 0)`, item.version).Scan(item.result); err != nil {
			return err
		}
	}
	if applied168 != 0 {
		return nil
	}
	if err := db.QueryRow(`SELECT instr(sql, '''fx''') FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`).Scan(&fxHarness); err != nil {
		return err
	}
	if applied163 != 0 && fxHarness != 0 {
		return nil
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('pr') WHERE name = 'discussion_comment_count'`).Scan(&discussionCountColumn); err != nil {
		return err
	}
	oldVersion := 0
	switch {
	case applied163 != 0 && fxHarness == 0:
		oldVersion = 163
	case applied159 != 0 && discussionCountColumn == 0:
		oldVersion = 159
	case applied156 != 0 && provisionColumns != 2:
		oldVersion = 156
	case applied155 != 0 && unrealHarness == 0 && provisionColumns != 2:
		oldVersion = 155
	case applied149 != 0 && reviewerColumn == 0 && provisionColumns != 2:
		oldVersion = 149
	case applied162 != 0:
		oldVersion = 162
	case applied161 != 0:
		oldVersion = 161
	}
	if oldVersion == 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (168, 1)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM goose_db_version WHERE version_id = ?`, oldVersion); err != nil {
		return err
	}
	return tx.Commit()
}

// repairRenumberedTaskProvisioningMigrationHistory preserves development
// databases that applied this branch's migrations at 0149/0150, 0150/0151,
// or 0155/0156/0157. Main owns those numbers now; map the physical schema to
// 0156/0157/0158 before Goose replays the missing main migrations.
func repairRenumberedTaskProvisioningMigrationHistory(db *sql.DB) error {
	shape, proceed, err := loadRenumberedProvisioningShape(db)
	if err != nil || !proceed {
		return err
	}
	provisionColumns, taskPreparationColumn, creationSHAColumn := shape.provisionColumns, shape.taskPreparationColumn, shape.creationSHAColumn
	reviewerColumn, catalogColumn, unrealHarness := shape.reviewerColumn, shape.catalogColumn, shape.unrealHarness

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	applied := func(version int64) (bool, error) {
		var applied int
		if err := tx.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = ? ORDER BY id DESC LIMIT 1
), 0)`, version).Scan(&applied); err != nil {
			return false, err
		}
		return applied == 1, nil
	}
	mapVersion := func(legacyVersion, canonicalVersion int64) (bool, error) {
		legacyApplied, err := applied(legacyVersion)
		if err != nil || !legacyApplied {
			return false, err
		}
		if _, err := tx.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, 1)`, canonicalVersion); err != nil {
			return false, err
		}
		if _, err := tx.Exec(`DELETE FROM goose_db_version WHERE version_id = ?`, legacyVersion); err != nil {
			return false, err
		}
		return true, nil
	}
	oldProvision155, err := applied(155)
	if err != nil {
		return err
	}
	if oldProvision155 && provisionColumns == 2 && unrealHarness == 0 {
		// This branch used 0155 before main shipped the Unreal Agent migration
		// at that number. Shift highest first so each applied marker survives.
		if creationSHAColumn != 0 {
			if _, err := mapVersion(157, 158); err != nil {
				return err
			}
		}
		if taskPreparationColumn != 0 {
			if _, err := mapVersion(156, 157); err != nil {
				return err
			}
		}
		if _, err := mapVersion(155, 156); err != nil {
			return err
		}
	}

	provisionMappedFrom150 := false
	canonicalProvisionApplied, err := applied(156)
	if err != nil {
		return err
	}
	if provisionColumns == 2 && !canonicalProvisionApplied {
		legacyVersion := int64(149)
		if reviewerColumn != 0 {
			// Once main's catalog column exists, 0150 belongs to main.
			if catalogColumn == 0 {
				legacyVersion = 150
			} else {
				legacyVersion = 0
			}
		}
		if legacyVersion != 0 {
			mapped, err := mapVersion(legacyVersion, 156)
			if err != nil {
				return err
			}
			provisionMappedFrom150 = mapped && legacyVersion == 150
		}
	}

	canonicalPreparationApplied, err := applied(157)
	if err != nil {
		return err
	}
	released150 := provisionMappedFrom150
	if taskPreparationColumn != 0 && !canonicalPreparationApplied {
		legacyVersion := int64(150)
		if catalogColumn != 0 || provisionMappedFrom150 {
			legacyVersion = 151
		}
		mapped, err := mapVersion(legacyVersion, 157)
		if err != nil {
			return err
		}
		released150 = released150 || mapped && legacyVersion == 150
	}
	if released150 && catalogColumn == 0 {
		// Main's 0151 may already have installed global catalog triggers. Drop
		// them before replaying 0150, then replay 0151 to restore global CDC.
		if _, err := tx.Exec(`DROP TRIGGER IF EXISTS agent_model_catalog_cdc_insert`); err != nil {
			return err
		}
		if _, err := tx.Exec(`DROP TRIGGER IF EXISTS agent_model_catalog_cdc_update`); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM goose_db_version WHERE version_id = 151`); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// repairRenumberedAgentInstallJobsMigrationHistory preserves development
// databases opened while the harness-installer branch owned versions 0119 or
// 0120. Main later assigned those versions to idempotent data repairs, so move
// the exact installer-table shape to its canonical 0123 ledger entry and let
// Goose replay the real 0119 and 0120 migrations.
func repairRenumberedAgentInstallJobsMigrationHistory(db *sql.DB) error {
	var gooseTable int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'`,
	).Scan(&gooseTable); err != nil {
		return err
	}
	if gooseTable == 0 {
		return nil
	}

	var installJobShape int
	if err := db.QueryRow(`
SELECT COUNT(*) FROM pragma_table_info('agent_install_jobs')
WHERE name IN (
    'target', 'status', 'method', 'command', 'expected_destination',
    'output', 'error', 'started_at', 'finished_at', 'updated_at'
)`).Scan(&installJobShape); err != nil {
		return err
	}
	if installJobShape != 10 {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var canonicalApplied int
	if err := tx.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = 123 ORDER BY id DESC LIMIT 1
), 0)`).Scan(&canonicalApplied); err != nil {
		return err
	}
	if canonicalApplied != 0 {
		return tx.Commit()
	}
	if _, err := tx.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (123, 1)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM goose_db_version WHERE version_id IN (119, 120)`); err != nil {
		return err
	}
	return tx.Commit()
}

// renumberedProvisioningShape is what the schema looks like before the
// renumbered task-provisioning migration history is repaired.
type renumberedProvisioningShape struct {
	provisionColumns, taskPreparationColumn, creationSHAColumn int
	reviewerColumn, catalogColumn, unrealHarness               int
}

// loadRenumberedProvisioningShape reads the schema facts the repair keys on.
// proceed is false when the database needs no repair.
func loadRenumberedProvisioningShape(db *sql.DB) (shape renumberedProvisioningShape, proceed bool, err error) {
	var gooseTable int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'`,
	).Scan(&gooseTable); err != nil {
		return shape, false, err
	}
	if gooseTable == 0 {
		return shape, false, nil
	}

	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name IN ('provision_state', 'provision_error')`,
	).Scan(&shape.provisionColumns); err != nil {
		return shape, false, err
	}
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name = 'is_task_preparation'`,
	).Scan(&shape.taskPreparationColumn); err != nil {
		return shape, false, err
	}
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('session_worktrees') WHERE name = 'creation_sha'`,
	).Scan(&shape.creationSHAColumn); err != nil {
		return shape, false, err
	}
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('review') WHERE name = 'interface_mode'`,
	).Scan(&shape.reviewerColumn); err != nil {
		return shape, false, err
	}
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('agent_model_catalog') WHERE name = 'metadata_json'`,
	).Scan(&shape.catalogColumn); err != nil {
		return shape, false, err
	}
	if shape.provisionColumns != 2 && shape.taskPreparationColumn == 0 {
		return shape, false, nil
	}
	if err := db.QueryRow(
		`SELECT instr(sql, 'unreal-agent') FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`,
	).Scan(&shape.unrealHarness); err != nil {
		return shape, false, err
	}
	return shape, true, nil
}
