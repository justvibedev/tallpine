-- report_quiet marks FYI reports that must not wake the orchestrator before
-- their (bounded) available_at. A separate table keeps the reports row shape
-- unchanged; the restart cleanup skips these rows.

-- +goose Up
CREATE TABLE report_quiet (report_id TEXT PRIMARY KEY REFERENCES reports(id) ON DELETE CASCADE);

-- +goose Down
DROP TABLE report_quiet;
