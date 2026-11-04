-- +goose Up

-- The session list the app polls (?active=true) and the periodic observers
-- read only live sessions, plus a per-project count of the archive. Both used
-- to decode every session ever recorded. Keyed by is_terminated so live rows
-- are a short range seek, and covering the archive count's GROUP BY so
-- counting history never touches the wide session rows.
CREATE INDEX idx_sessions_terminated
    ON sessions(is_terminated, project_id, kind, is_task_preparation);

-- +goose Down
DROP INDEX IF EXISTS idx_sessions_terminated;
