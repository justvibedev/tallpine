-- +goose Up
-- +goose StatementBegin
-- Automatic cleanup (Settings, Cleanup; `tallpine cleanup`).
--
-- The policy is one JSON document so a rule added later brings its own keys
-- without a migration: a key the document lacks reads as that rule's default.
-- '{}' is therefore "all defaults". CHECK (id = 1) keeps the row a singleton.
-- IF NOT EXISTS: a dev data dir may have applied this migration under an
-- earlier version number before it was renumbered.
CREATE TABLE IF NOT EXISTS cleanup_settings (
    id         INTEGER PRIMARY KEY CHECK (id = 1),
    policy     TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(policy)),
    updated_at TIMESTAMP NOT NULL
);

INSERT OR IGNORE INTO cleanup_settings (id, policy, updated_at) VALUES (1, '{}', CURRENT_TIMESTAMP);

-- One row per cleanup pass, real or dry run, so the user can see what was
-- freed and when. The report is the pass's per-category summary and a bounded
-- list of items; the daemon keeps a bounded number of rows.
CREATE TABLE IF NOT EXISTS cleanup_runs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    started_at  TIMESTAMP NOT NULL,
    finished_at TIMESTAMP NOT NULL,
    trigger     TEXT NOT NULL CHECK (trigger IN ('schedule', 'manual')),
    pass        TEXT NOT NULL CHECK (pass IN ('light', 'full')),
    dry_run     INTEGER NOT NULL CHECK (dry_run IN (0, 1)),
    freed_bytes INTEGER NOT NULL DEFAULT 0,
    report      TEXT NOT NULL CHECK (json_valid(report)),
    error       TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_cleanup_runs_dry_run_id ON cleanup_runs(dry_run, id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS cleanup_runs;
DROP TABLE IF EXISTS cleanup_settings;
-- +goose StatementEnd
