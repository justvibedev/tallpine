-- Summary: persist owner intents and standing decisions for a project's orchestrator so they survive compaction and handoff.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE orchestrator_intents (
    id         TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    text       TEXT NOT NULL,
    created_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL,
    done_at    TIMESTAMP
);
CREATE INDEX orchestrator_intents_project ON orchestrator_intents(project_id, created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS orchestrator_intents;
-- +goose StatementEnd
