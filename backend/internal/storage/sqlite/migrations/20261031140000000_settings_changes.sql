-- Settings change log (B202): one row per changed setting key, with who
-- changed it. actor is "user" or "<role>:<session id>" for an agent token.
-- project_id is empty for app-wide settings. Values are JSON; a sensitive
-- setting (project env) stores a redaction marker instead of its value.

-- +goose Up
CREATE TABLE settings_changes (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    changed_at  TIMESTAMP NOT NULL,
    actor       TEXT NOT NULL,
    project_id  TEXT NOT NULL DEFAULT '',
    setting_key TEXT NOT NULL,
    old_value   TEXT NOT NULL,
    new_value   TEXT NOT NULL
);
CREATE INDEX settings_changes_project_time ON settings_changes (project_id, changed_at);

-- +goose Down
DROP TABLE settings_changes;
