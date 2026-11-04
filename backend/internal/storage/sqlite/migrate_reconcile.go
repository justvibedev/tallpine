package sqlite

import (
	"database/sql"
	"fmt"
	"strings"
)

// schemaRepairs lists the column-level effects of migrations that real
// installs are known to skip. Issue #3475/#3476: profiles exist whose
// goose_db_version already records versions 40 through 46 (written by a
// foreign build), so goose silently skips the real migrations carrying those
// numbers and the generated queries then fail with "no such column" — every
// session list 500s while /healthz stays green. A versioned repair migration
// cannot fix this class, because a burned version number is exactly what
// caused it; instead the physical schema is verified on every startup.
//
// Each entry keys on one column. postAdd statements replay the rest of the
// skipped migration's effects (backfills, index swaps) and run ONLY when the
// column was just added, so healthy databases — where those statements would
// clobber live data — are never touched.
//
// Any migration whose schema the generated queries depend on MUST add an entry
// here when a burned field profile can skip it, or the session list can regress
// to the 500s this exists to prevent.
var schemaRepairs = []struct {
	version int64
	table   string
	column  string
	addDDL  string
	postAdd []string
}{
	// 0040_add_session_diff_base.sql
	{version: 40, table: "sessions", column: "diff_base_sha",
		addDDL: `ALTER TABLE sessions ADD COLUMN diff_base_sha TEXT NOT NULL DEFAULT ''`},
	{version: 40, table: "sessions", column: "diff_base_ref",
		addDDL: `ALTER TABLE sessions ADD COLUMN diff_base_ref TEXT NOT NULL DEFAULT ''`},
	// 0041_notification_resolution.sql
	{version: 41, table: "notifications", column: "resolved_at",
		addDDL: `ALTER TABLE notifications ADD COLUMN resolved_at TIMESTAMP`,
		postAdd: []string{
			`UPDATE notifications SET resolved_at = created_at WHERE status = 'read'`,
			`DROP INDEX IF EXISTS idx_notifications_unread_dedupe`,
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_notifications_open_dedupe
    ON notifications(session_id, type, pr_url)
    WHERE status = 'unread' OR resolved_at IS NULL`,
			`CREATE INDEX IF NOT EXISTS idx_notifications_unresolved
    ON notifications(resolved_at, created_at DESC, id DESC)`,
		}},
	// 0042_review_run_unique_per_harness.sql
	{version: 42, table: "sessions", column: "reviewer_harness",
		addDDL: `ALTER TABLE sessions ADD COLUMN reviewer_harness TEXT NOT NULL DEFAULT ''`,
		postAdd: []string{
			`DROP INDEX IF EXISTS idx_review_run_session_pr_sha`,
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_review_run_session_pr_sha_harness
    ON review_run (session_id, pr_url, target_sha, harness)
    WHERE target_sha != ''
        AND status NOT IN ('failed', 'cancelled')
        AND (status = 'running' OR verdict NOT IN ('', 'changes_requested'))`,
		}},
	// 0043_add_session_pinned.sql. The trigger replay hangs off pinned_at, the
	// second of the two columns: it references both, and SQLite resolves a
	// trigger body at CREATE time, so it cannot run until both exist.
	{version: 43, table: "sessions", column: "is_pinned",
		addDDL: `ALTER TABLE sessions ADD COLUMN is_pinned BOOLEAN NOT NULL DEFAULT 0`},
	{version: 43, table: "sessions", column: "pinned_at",
		addDDL: `ALTER TABLE sessions ADD COLUMN pinned_at DATETIME`,
		postAdd: []string{
			`DROP TRIGGER IF EXISTS sessions_cdc_update`,
			`CREATE TRIGGER sessions_cdc_update
AFTER UPDATE ON sessions
WHEN OLD.activity_state <> NEW.activity_state
    OR OLD.is_terminated <> NEW.is_terminated
    OR (OLD.first_signal_at IS NULL AND NEW.first_signal_at IS NOT NULL)
    OR OLD.preview_url <> NEW.preview_url
    OR OLD.preview_revision <> NEW.preview_revision
    OR OLD.display_name <> NEW.display_name
    OR OLD.terminate_on_pr_merge <> NEW.terminate_on_pr_merge
    OR OLD.is_pinned <> NEW.is_pinned
    OR OLD.pinned_at <> NEW.pinned_at
    OR (OLD.pinned_at IS NULL AND NEW.pinned_at IS NOT NULL)
    OR (OLD.pinned_at IS NOT NULL AND NEW.pinned_at IS NULL)
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NEW.id, 'session_updated',
        json_object(
            'id', NEW.id,
            'activity', NEW.activity_state,
            'isTerminated', json(CASE WHEN NEW.is_terminated THEN 'true' ELSE 'false' END),
            'terminateOnPrMerge', json(CASE WHEN NEW.terminate_on_pr_merge THEN 'true' ELSE 'false' END),
            'previewUrl', NEW.preview_url,
            'previewRevision', NEW.preview_revision,
            'isPinned', json(CASE WHEN NEW.is_pinned THEN 'true' ELSE 'false' END)
        ),
        NEW.updated_at);
			END`,
		}},
	// 0081_browser_capability_verifier.sql. Keep the generated session queries
	// healthy even if a field database has already burned this migration number.
	{version: 81, table: "sessions", column: "browser_capability_verifier",
		addDDL: `ALTER TABLE sessions ADD COLUMN browser_capability_verifier TEXT NOT NULL DEFAULT ''`},
	// A pre-renumbered chat-mode branch created conversations before the
	// current_session_id controller binding existed, then later builds recorded
	// 0052 as applied. Generated chat queries require the column on startup.
	{version: 66, table: "conversations", column: "current_session_id",
		addDDL: `ALTER TABLE conversations ADD COLUMN current_session_id TEXT REFERENCES sessions(id) ON DELETE SET NULL`,
		postAdd: []string{
			`UPDATE conversations SET current_session_id = session_id WHERE current_session_id IS NULL AND session_id IS NOT NULL`,
			`CREATE INDEX IF NOT EXISTS idx_conversations_current_session ON conversations(current_session_id)
    WHERE current_session_id IS NOT NULL`,
		}},
	// Version 86 was briefly used by queued-turn promotion on a development
	// branch. Ensure those databases also receive upstream's version-86 effect.
	{version: 86, table: "workspace_repos", column: "default_branch",
		addDDL: `ALTER TABLE workspace_repos ADD COLUMN default_branch TEXT NOT NULL DEFAULT ''`},
	// These columns are referenced by every generated conversation turn/message
	// query, so verify their physical presence on every startup.
	{version: 89, table: "conversation_turns", column: "promotion_started_at",
		addDDL: `ALTER TABLE conversation_turns ADD COLUMN promotion_started_at TIMESTAMP`},
	{version: 89, table: "conversation_turns", column: "promoted_to_turn_id",
		addDDL: `ALTER TABLE conversation_turns ADD COLUMN promoted_to_turn_id TEXT REFERENCES conversation_turns(id) ON DELETE SET NULL`},
	// 0106_pr_comment_review_id.sql. Session projections join this column, so a
	// field database that recorded 0106 without its physical effect must be
	// repaired before the first list request.
	{version: 106, table: "pr_comment", column: "review_id",
		addDDL: `ALTER TABLE pr_comment ADD COLUMN review_id TEXT NOT NULL DEFAULT ''`},
	// 0108_conversation_retry_source.sql. Some stacked development builds
	// recorded 0108 without applying its physical schema; 0118 rebuilds this
	// table and must see the retry lineage column before goose reaches it.
	{version: 108, table: "conversation_turns", column: "retry_of_turn_id",
		addDDL: `ALTER TABLE conversation_turns ADD COLUMN retry_of_turn_id TEXT REFERENCES conversation_turns(id) ON DELETE RESTRICT`,
		postAdd: []string{
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_conversation_turns_retry_source
    ON conversation_turns(conversation_id, retry_of_turn_id)
    WHERE retry_of_turn_id IS NOT NULL`,
		}},
	// 0130_pr_review_partial.sql. Generated PR reads select this column, so a
	// field database that burned version 130 must not lose it. The default
	// matches the migration: unknown historical certainty stays partial.
	{version: 130, table: "pr", column: "review_partial",
		addDDL: `ALTER TABLE pr ADD COLUMN review_partial BOOLEAN NOT NULL DEFAULT TRUE`},
	// 0132_conversation_opencode_mode.sql. Generated conversation reads select
	// this column, so repair field databases that recorded 0132 without adding it.
	{version: 132, table: "conversations", column: "opencode_mode",
		addDDL: `ALTER TABLE conversations ADD COLUMN opencode_mode TEXT NOT NULL DEFAULT ''`},
}

// reconcileSchema verifies that the columns in schemaRepairs physically exist
// and replays the skipped migration's effects for any that are missing. It is
// idempotent: a healthy database (migrations applied normally, or one already
// repaired by hand or a previous startup) is left untouched. Failures surface
// as a specific, actionable startup error instead of an opaque INTERNAL_ERROR
// on the first session list.
func reconcileSchema(db *sql.DB) error {
	for _, rc := range schemaRepairs {
		var count int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, rc.table, rc.column,
		).Scan(&count); err != nil {
			return fmt.Errorf("schema verification: inspect %s.%s: %w", rc.table, rc.column, err)
		}
		if count > 0 {
			continue
		}
		if _, err := db.Exec(rc.addDDL); err != nil {
			return fmt.Errorf(
				"schema repair: %s.%s is missing (a burned goose version skipped the migration that adds it, see #3475) and could not be added: %w",
				rc.table, rc.column, err,
			)
		}
		for _, stmt := range rc.postAdd {
			if _, err := db.Exec(stmt); err != nil {
				return fmt.Errorf("schema repair: replay skipped migration effects for %s.%s: %w", rc.table, rc.column, err)
			}
		}
	}
	return reconcileHarnessConstraint(db)
}

const (
	sessionsHarnessCheckWithoutMuse                   = `CHECK (harness IN ('', 'claude-code', 'codex', 'aider', 'opencode', 'grok', 'droid', 'amp', 'agy', 'crush', 'cursor', 'qwen', 'copilot', 'goose', 'auggie', 'continue', 'devin', 'cline', 'kimi', 'kiro', 'kilocode', 'vibe', 'pi', 'autohand', 'fake'))`
	sessionsHarnessCheckWithMuse                      = `CHECK (harness IN ('', 'claude-code', 'codex', 'aider', 'opencode', 'grok', 'droid', 'amp', 'agy', 'crush', 'cursor', 'qwen', 'copilot', 'goose', 'auggie', 'continue', 'devin', 'cline', 'kimi', 'muse', 'kiro', 'kilocode', 'vibe', 'pi', 'autohand', 'fake'))`
	sessionsHarnessCheckWithoutMuseQM                 = `CHECK (harness IN ('', 'claude-code', 'codex', 'aider', 'opencode', 'grok', 'droid', 'amp', 'agy', 'crush', 'cursor', 'qwen', 'copilot', 'goose', 'auggie', 'continue', 'devin', 'cline', 'kimi', 'kiro', 'kilocode', 'vibe', 'pi', 'autohand', 'qm', 'fake'))`
	sessionsHarnessCheckWithMuseQM                    = `CHECK (harness IN ('', 'claude-code', 'codex', 'aider', 'opencode', 'grok', 'droid', 'amp', 'agy', 'crush', 'cursor', 'qwen', 'copilot', 'goose', 'auggie', 'continue', 'devin', 'cline', 'kimi', 'muse', 'kiro', 'kilocode', 'vibe', 'pi', 'autohand', 'qm', 'fake'))`
	sessionsHarnessCheckWithMuseKimchi                = `CHECK (harness IN ('', 'claude-code', 'codex', 'aider', 'opencode', 'grok', 'droid', 'amp', 'agy', 'crush', 'cursor', 'qwen', 'copilot', 'goose', 'auggie', 'continue', 'devin', 'cline', 'kimi', 'muse', 'kiro', 'kilocode', 'vibe', 'pi', 'kimchi', 'autohand', 'fake'))`
	sessionsHarnessCheckWithMuseQMKimchi              = `CHECK (harness IN ('', 'claude-code', 'codex', 'aider', 'opencode', 'grok', 'droid', 'amp', 'agy', 'crush', 'cursor', 'qwen', 'copilot', 'goose', 'auggie', 'continue', 'devin', 'cline', 'kimi', 'muse', 'kiro', 'kilocode', 'vibe', 'pi', 'kimchi', 'autohand', 'qm', 'fake'))`
	sessionsHarnessCheckWithMusePrimeAgent            = `CHECK (harness IN ('', 'claude-code', 'codex', 'aider', 'opencode', 'grok', 'droid', 'amp', 'agy', 'crush', 'cursor', 'qwen', 'copilot', 'goose', 'auggie', 'continue', 'devin', 'cline', 'kimi', 'muse', 'kiro', 'kilocode', 'vibe', 'pi', 'prime-agent', 'autohand', 'fake'))`
	sessionsHarnessCheckWithMuseQMPrimeAgent          = `CHECK (harness IN ('', 'claude-code', 'codex', 'aider', 'opencode', 'grok', 'droid', 'amp', 'agy', 'crush', 'cursor', 'qwen', 'copilot', 'goose', 'auggie', 'continue', 'devin', 'cline', 'kimi', 'muse', 'kiro', 'kilocode', 'vibe', 'pi', 'prime-agent', 'autohand', 'qm', 'fake'))`
	sessionsHarnessCheckWithMuseQMLegacyPrimeAgent    = `CHECK (harness IN ('', 'claude-code', 'codex', 'aider', 'opencode', 'grok', 'droid', 'amp', 'agy', 'crush', 'cursor', 'qwen', 'copilot', 'goose', 'auggie', 'continue', 'devin', 'cline', 'kimi', 'muse', 'kiro', 'kilocode', 'vibe', 'pi', 'autohand', 'qm', 'prime-agent', 'fake'))`
	sessionsHarnessCheckWithMuseKimchiPrimeAgent      = `CHECK (harness IN ('', 'claude-code', 'codex', 'aider', 'opencode', 'grok', 'droid', 'amp', 'agy', 'crush', 'cursor', 'qwen', 'copilot', 'goose', 'auggie', 'continue', 'devin', 'cline', 'kimi', 'muse', 'kiro', 'kilocode', 'vibe', 'pi', 'kimchi', 'prime-agent', 'autohand', 'fake'))`
	sessionsHarnessCheckWithMuseQMKimchiPrimeAgent    = `CHECK (harness IN ('', 'claude-code', 'codex', 'aider', 'opencode', 'grok', 'droid', 'amp', 'agy', 'crush', 'cursor', 'qwen', 'copilot', 'goose', 'auggie', 'continue', 'devin', 'cline', 'kimi', 'muse', 'kiro', 'kilocode', 'vibe', 'pi', 'kimchi', 'prime-agent', 'autohand', 'qm', 'fake'))`
	sessionsHarnessCheckWithMuseKimchiPrimeAgentOMP   = `CHECK (harness IN ('', 'claude-code', 'codex', 'aider', 'opencode', 'grok', 'droid', 'amp', 'agy', 'crush', 'cursor', 'qwen', 'copilot', 'goose', 'auggie', 'continue', 'devin', 'cline', 'kimi', 'muse', 'kiro', 'kilocode', 'vibe', 'pi', 'kimchi', 'prime-agent', 'autohand', 'omp', 'fake'))`
	sessionsHarnessCheckWithMuseQMKimchiPrimeAgentOMP = `CHECK (harness IN ('', 'claude-code', 'codex', 'aider', 'opencode', 'grok', 'droid', 'amp', 'agy', 'crush', 'cursor', 'qwen', 'copilot', 'goose', 'auggie', 'continue', 'devin', 'cline', 'kimi', 'muse', 'kiro', 'kilocode', 'vibe', 'pi', 'kimchi', 'prime-agent', 'autohand', 'omp', 'qm', 'fake'))`
)

const (
	sessionsHarnessCheckWithMuseKimchiPrimeAgentOMPUnreal         = `CHECK (harness IN ('', 'claude-code', 'codex', 'aider', 'opencode', 'grok', 'droid', 'amp', 'agy', 'crush', 'cursor', 'qwen', 'copilot', 'goose', 'auggie', 'continue', 'devin', 'cline', 'kimi', 'muse', 'kiro', 'kilocode', 'vibe', 'pi', 'kimchi', 'prime-agent', 'autohand', 'omp', 'unreal-agent', 'fake'))`
	sessionsHarnessCheckWithMuseQMKimchiPrimeAgentOMPUnreal       = `CHECK (harness IN ('', 'claude-code', 'codex', 'aider', 'opencode', 'grok', 'droid', 'amp', 'agy', 'crush', 'cursor', 'qwen', 'copilot', 'goose', 'auggie', 'continue', 'devin', 'cline', 'kimi', 'muse', 'kiro', 'kilocode', 'vibe', 'pi', 'kimchi', 'prime-agent', 'autohand', 'omp', 'unreal-agent', 'qm', 'fake'))`
	sessionsHarnessCheckWithMuseKimchiPrimeAgentOMPUnrealMiMo     = `CHECK (harness IN ('', 'claude-code', 'codex', 'aider', 'opencode', 'grok', 'droid', 'amp', 'agy', 'crush', 'cursor', 'qwen', 'copilot', 'goose', 'auggie', 'continue', 'devin', 'cline', 'kimi', 'muse', 'kiro', 'kilocode', 'vibe', 'pi', 'kimchi', 'prime-agent', 'autohand', 'omp', 'unreal-agent', 'mimo-code', 'fake'))`
	sessionsHarnessCheckWithMuseQMKimchiPrimeAgentOMPUnrealMiMo   = `CHECK (harness IN ('', 'claude-code', 'codex', 'aider', 'opencode', 'grok', 'droid', 'amp', 'agy', 'crush', 'cursor', 'qwen', 'copilot', 'goose', 'auggie', 'continue', 'devin', 'cline', 'kimi', 'muse', 'kiro', 'kilocode', 'vibe', 'pi', 'kimchi', 'prime-agent', 'autohand', 'omp', 'unreal-agent', 'mimo-code', 'qm', 'fake'))`
	sessionsHarnessCheckWithMuseKimchiPrimeAgentOMPUnrealFX       = `CHECK (harness IN ('', 'claude-code', 'codex', 'aider', 'opencode', 'grok', 'droid', 'amp', 'agy', 'crush', 'cursor', 'qwen', 'copilot', 'goose', 'auggie', 'continue', 'devin', 'cline', 'kimi', 'muse', 'kiro', 'kilocode', 'vibe', 'pi', 'kimchi', 'prime-agent', 'autohand', 'omp', 'unreal-agent', 'fx', 'fake'))`
	sessionsHarnessCheckWithMuseQMKimchiPrimeAgentOMPUnrealFX     = `CHECK (harness IN ('', 'claude-code', 'codex', 'aider', 'opencode', 'grok', 'droid', 'amp', 'agy', 'crush', 'cursor', 'qwen', 'copilot', 'goose', 'auggie', 'continue', 'devin', 'cline', 'kimi', 'muse', 'kiro', 'kilocode', 'vibe', 'pi', 'kimchi', 'prime-agent', 'autohand', 'omp', 'unreal-agent', 'fx', 'qm', 'fake'))`
	sessionsHarnessCheckWithMuseKimchiPrimeAgentOMPUnrealFXMiMo   = `CHECK (harness IN ('', 'claude-code', 'codex', 'aider', 'opencode', 'grok', 'droid', 'amp', 'agy', 'crush', 'cursor', 'qwen', 'copilot', 'goose', 'auggie', 'continue', 'devin', 'cline', 'kimi', 'muse', 'kiro', 'kilocode', 'vibe', 'pi', 'kimchi', 'prime-agent', 'autohand', 'omp', 'unreal-agent', 'fx', 'mimo-code', 'fake'))`
	sessionsHarnessCheckWithMuseQMKimchiPrimeAgentOMPUnrealFXMiMo = `CHECK (harness IN ('', 'claude-code', 'codex', 'aider', 'opencode', 'grok', 'droid', 'amp', 'agy', 'crush', 'cursor', 'qwen', 'copilot', 'goose', 'auggie', 'continue', 'devin', 'cline', 'kimi', 'muse', 'kiro', 'kilocode', 'vibe', 'pi', 'kimchi', 'prime-agent', 'autohand', 'omp', 'unreal-agent', 'fx', 'mimo-code', 'qm', 'fake'))`
)

func reconcileHarnessConstraint(db *sql.DB) error {
	var schema string
	if err := db.QueryRow(
		`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`,
	).Scan(&schema); err != nil {
		return fmt.Errorf("schema verification: inspect sessions harness constraint: %w", err)
	}
	needsNativeAssistant := !strings.Contains(schema, "'hermes'")
	needsMuse := !strings.Contains(schema, "'muse'")
	needsKimchi := !strings.Contains(schema, "'kimchi'")
	needsPrimeAgent := !strings.Contains(schema, "'prime-agent'")
	needsOMP := !strings.Contains(schema, "'omp'")
	needsGemini := !strings.Contains(schema, "'gemini'")
	needsUnreal := !strings.Contains(schema, "'unreal-agent'")
	needsCodewhale := !strings.Contains(schema, "'codewhale'")
	needsMiMo := !strings.Contains(schema, "'mimo-code'")
	needsDeepSeek := !strings.Contains(schema, "'deepseek-harness'")
	needsOpenHands := !strings.Contains(schema, "'openhands'")
	needsMemcode := !strings.Contains(schema, "'memcode'")
	needsCommandCode := !strings.Contains(schema, "'command-code'")
	if !needsNativeAssistant && !needsMemcode && !needsMuse && !needsKimchi && !needsPrimeAgent && !needsOMP && !needsGemini && !needsUnreal && !needsCodewhale && !needsMiMo && !needsDeepSeek && !needsOpenHands && !needsCommandCode {
		return nil
	}
	if _, err := db.Exec(`PRAGMA writable_schema = ON`); err != nil {
		return fmt.Errorf("schema repair: enable writable_schema for sessions harness constraint: %w", err)
	}
	if !needsNativeAssistant {
		// Older constraints need exact historical repairs. Temporarily remove
		// Hermes wherever it appears before the final fake entry, apply those
		// repairs, then append Hermes below without removing later harnesses.
		if _, err := db.Exec(`UPDATE sqlite_master SET sql = replace(sql, '''hermes'', ', '') WHERE type = 'table' AND name = 'sessions'`); err != nil {
			return fmt.Errorf("schema repair: normalize Hermes constraint: %w", err)
		}
		needsNativeAssistant = true
	}
	repairs := harnessConstraintRepairs(harnessNeeds{
		nativeAssistant: needsNativeAssistant, muse: needsMuse, kimchi: needsKimchi, primeAgent: needsPrimeAgent, omp: needsOMP,
		gemini: needsGemini, unreal: needsUnreal, codewhale: needsCodewhale, mimo: needsMiMo, deepSeek: needsDeepSeek,
		openHands: needsOpenHands, memcode: needsMemcode, commandCode: needsCommandCode,
	})
	for _, r := range repairs {
		if _, err := db.Exec(
			`UPDATE sqlite_master
SET sql = replace(sql, ?, ?)
WHERE type = 'table' AND name = 'sessions'`,
			r.old,
			r.new,
		); err != nil {
			return fmt.Errorf("schema repair: widen sessions harness constraint: %w", err)
		}
	}
	if _, err := db.Exec(`PRAGMA writable_schema = RESET`); err != nil {
		return fmt.Errorf("schema repair: reparse sessions harness constraint: %w", err)
	}
	if err := db.QueryRow(
		`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`,
	).Scan(&schema); err != nil {
		return fmt.Errorf("schema verification: inspect repaired sessions harness constraint: %w", err)
	}
	return verifyHarnessConstraint(schema)
}

// harnessReplacement is one exact-string repair of the sessions harness CHECK.
type harnessReplacement struct {
	old string
	new string
}

// harnessNeeds records which harness names the sessions CHECK constraint lacks.
type harnessNeeds struct {
	nativeAssistant, muse, kimchi, primeAgent, omp, gemini, unreal bool
	codewhale, mimo, deepSeek, openHands, memcode, commandCode     bool
}

// harnessConstraintRepairs lists the repairs, in the order they must run, that
// widen the sessions harness constraint to every harness in needs.
func harnessConstraintRepairs(needs harnessNeeds) []harnessReplacement {
	var repairs []harnessReplacement
	if needs.muse {
		repairs = append(repairs,
			harnessReplacement{sessionsHarnessCheckWithoutMuse, sessionsHarnessCheckWithMuse},
			harnessReplacement{sessionsHarnessCheckWithoutMuseQM, sessionsHarnessCheckWithMuseQM},
		)
	}
	if needs.kimchi {
		// After the Muse repair (if any), the constraint will be in one of
		// these known states — each gets Kimchi inserted in the same
		// position as migration 0054 does.
		repairs = append(repairs,
			harnessReplacement{sessionsHarnessCheckWithMuse, sessionsHarnessCheckWithMuseKimchi},
			harnessReplacement{sessionsHarnessCheckWithMuseQM, sessionsHarnessCheckWithMuseQMKimchi},
			harnessReplacement{sessionsHarnessCheckWithMusePrimeAgent, sessionsHarnessCheckWithMuseKimchiPrimeAgent},
			harnessReplacement{sessionsHarnessCheckWithMuseQMPrimeAgent, sessionsHarnessCheckWithMuseQMKimchiPrimeAgent},
			harnessReplacement{sessionsHarnessCheckWithMuseQMLegacyPrimeAgent, sessionsHarnessCheckWithMuseQMKimchiPrimeAgent},
		)
	}
	if needs.primeAgent {
		repairs = append(repairs,
			harnessReplacement{sessionsHarnessCheckWithMuseKimchi, sessionsHarnessCheckWithMuseKimchiPrimeAgent},
			harnessReplacement{sessionsHarnessCheckWithMuseQMKimchi, sessionsHarnessCheckWithMuseQMKimchiPrimeAgent},
		)
	}
	if needs.omp {
		repairs = append(repairs,
			harnessReplacement{sessionsHarnessCheckWithMuseKimchiPrimeAgent, sessionsHarnessCheckWithMuseKimchiPrimeAgentOMP},
			harnessReplacement{sessionsHarnessCheckWithMuseQMKimchiPrimeAgent, sessionsHarnessCheckWithMuseQMKimchiPrimeAgentOMP},
		)
	}
	if needs.gemini {
		// Goose runs before reconciliation. A legacy constraint can therefore
		// miss migration 0163, then reach the OMP shape through repairs above.
		// Widen both known variants here without dropping the legacy QM value.
		for _, old := range []string{
			sessionsHarnessCheckWithMuseKimchiPrimeAgentOMP,
			sessionsHarnessCheckWithMuseQMKimchiPrimeAgentOMP,
			sessionsHarnessCheckWithMuseKimchiPrimeAgentOMPUnreal,
			sessionsHarnessCheckWithMuseQMKimchiPrimeAgentOMPUnreal,
			sessionsHarnessCheckWithMuseKimchiPrimeAgentOMPUnrealFX,
			sessionsHarnessCheckWithMuseQMKimchiPrimeAgentOMPUnrealFX,
			sessionsHarnessCheckWithMuseKimchiPrimeAgentOMPUnrealMiMo,
			sessionsHarnessCheckWithMuseQMKimchiPrimeAgentOMPUnrealMiMo,
			sessionsHarnessCheckWithMuseKimchiPrimeAgentOMPUnrealFXMiMo,
			sessionsHarnessCheckWithMuseQMKimchiPrimeAgentOMPUnrealFXMiMo,
		} {
			repairs = append(repairs, harnessReplacement{old, strings.Replace(old, "'omp'", "'gemini', 'omp'", 1)})
		}
	}
	if needs.unreal {
		repairs = append(repairs,
			harnessReplacement{sessionsHarnessCheckWithMuseKimchiPrimeAgentOMP, sessionsHarnessCheckWithMuseKimchiPrimeAgentOMPUnreal},
			harnessReplacement{sessionsHarnessCheckWithMuseQMKimchiPrimeAgentOMP, sessionsHarnessCheckWithMuseQMKimchiPrimeAgentOMPUnreal},
		)
		for _, old := range []string{sessionsHarnessCheckWithMuseKimchiPrimeAgentOMP, sessionsHarnessCheckWithMuseQMKimchiPrimeAgentOMP} {
			withGemini := strings.Replace(old, "'omp'", "'gemini', 'omp'", 1)
			repairs = append(repairs, harnessReplacement{withGemini, strings.Replace(withGemini, "'omp'", "'omp', 'unreal-agent'", 1)})
		}
	}
	if needs.mimo {
		withoutGemini := []harnessReplacement{
			harnessReplacement{sessionsHarnessCheckWithMuseKimchiPrimeAgentOMPUnreal, sessionsHarnessCheckWithMuseKimchiPrimeAgentOMPUnrealMiMo},
			harnessReplacement{sessionsHarnessCheckWithMuseQMKimchiPrimeAgentOMPUnreal, sessionsHarnessCheckWithMuseQMKimchiPrimeAgentOMPUnrealMiMo},
			harnessReplacement{sessionsHarnessCheckWithMuseKimchiPrimeAgentOMPUnrealFX, sessionsHarnessCheckWithMuseKimchiPrimeAgentOMPUnrealFXMiMo},
			harnessReplacement{sessionsHarnessCheckWithMuseQMKimchiPrimeAgentOMPUnrealFX, sessionsHarnessCheckWithMuseQMKimchiPrimeAgentOMPUnrealFXMiMo},
		}
		for _, r := range withoutGemini {
			repairs = append(repairs, r, harnessReplacement{
				strings.Replace(r.old, "'omp'", "'gemini', 'omp'", 1),
				strings.Replace(r.new, "'omp'", "'gemini', 'omp'", 1),
			})
		}
	}
	if needs.deepSeek {
		// Migration 0166 rewrites the current constraint variants by exact string.
		// A database that skipped an earlier harness migration matches none of
		// them, so it reaches this repair with the harness list still missing
		// entries; goose has already run, so nothing else adds this harness. Every
		// variant ends with the retained 'fake' fixture harness, so anchor there
		// instead of enumerating the shapes repaired above.
		repairs = append(repairs, harnessReplacement{"'fake'))", "'deepseek-harness', 'fake'))"})
	}
	if needs.openHands {
		// Migration 0192 rewrites the current constraint variants by exact
		// string. A database that skipped an earlier harness migration matches
		// none of them, so it reaches this repair with the harness list still
		// missing entries; goose has already run, so nothing else adds this
		// harness. Every variant ends with the retained 'fake' fixture harness,
		// so anchor there instead of enumerating the shapes repaired above.
		// This runs after the DeepSeek repair, so a database missing both gets
		// 'deepseek-harness', 'openhands', 'fake' in that order.
		repairs = append(repairs, harnessReplacement{"'fake'))", "'openhands', 'fake'))"})
	}
	if needs.codewhale {
		repairs = append(repairs, harnessReplacement{"'fake'))", "'codewhale', 'fake'))"})
	}
	if needs.commandCode {
		// Migration 0194 rewrites the constraint by anchoring on the retained
		// 'fake' fixture harness, for the same reason as DeepSeek above. A
		// database that skipped an earlier harness migration reaches this repair
		// without Command Code, so anchor there instead of enumerating shapes.
		repairs = append(repairs, harnessReplacement{"'fake'))", "'command-code', 'fake'))"})
	}
	// Add the new harness only after exact historical variants are repaired.
	if needs.nativeAssistant {
		repairs = append(repairs, harnessReplacement{"'fake'))", "'hermes', 'fake'))"})
	}
	if needs.memcode {
		repairs = append(repairs, harnessReplacement{"'fake'))", "'memcode', 'fake'))"})
	}
	return repairs
}

// verifyHarnessConstraint checks the repaired constraint names every harness.
func verifyHarnessConstraint(schema string) error {
	if !strings.Contains(schema, "'muse'") {
		return fmt.Errorf("schema repair: sessions harness constraint is missing Muse and did not match known pre-Muse schema")
	}
	if !strings.Contains(schema, "'kimchi'") {
		return fmt.Errorf("schema repair: sessions harness constraint is missing Kimchi and did not match known pre-Kimchi schema")
	}
	if !strings.Contains(schema, "'prime-agent'") {
		return fmt.Errorf("schema repair: sessions harness constraint is missing Prime Agent and did not match known pre-Prime-Agent schema")
	}
	if !strings.Contains(schema, "'omp'") {
		return fmt.Errorf("schema repair: sessions harness constraint is missing OMP and did not match known pre-OMP schema")
	}
	if !strings.Contains(schema, "'gemini'") {
		return fmt.Errorf("schema repair: sessions harness constraint is missing Gemini and did not match known pre-Gemini schema")
	}
	if !strings.Contains(schema, "'unreal-agent'") {
		return fmt.Errorf("schema repair: sessions harness constraint is missing Unreal Agent and did not match known pre-Unreal-Agent schema")
	}
	if !strings.Contains(schema, "'mimo-code'") {
		return fmt.Errorf("schema repair: sessions harness constraint is missing MiMo Code and did not match known pre-MiMo-Code schema")
	}
	if !strings.Contains(schema, "'deepseek-harness'") {
		return fmt.Errorf("schema repair: sessions harness constraint is missing DeepSeek Harness and did not match known pre-DeepSeek schema")
	}
	if !strings.Contains(schema, "'codewhale'") {
		return fmt.Errorf("schema repair: sessions harness constraint is missing Codewhale")
	}
	if !strings.Contains(schema, "'openhands'") {
		return fmt.Errorf("schema repair: sessions harness constraint is missing OpenHands and did not match known pre-OpenHands schema")
	}
	if !strings.Contains(schema, "'command-code'") {
		return fmt.Errorf("schema repair: sessions harness constraint is missing Command Code and did not match known pre-Command-Code schema")
	}
	if !strings.Contains(schema, "'memcode'") {
		return fmt.Errorf("schema repair: sessions harness constraint is missing Memcode and did not match known pre-Memcode schema")
	}
	if !strings.Contains(schema, "'hermes'") {
		return fmt.Errorf("schema repair: sessions harness constraint is missing hermes")
	}
	return nil
}
