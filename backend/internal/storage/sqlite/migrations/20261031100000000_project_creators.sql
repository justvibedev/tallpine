-- Cross-project orchestration: which project's orchestrator registered a
-- project. Only that creator project's orchestrator may later configure the
-- project or start its orchestrator (agent policy crossProjectOrchestration).
-- A project registered by a person has no row.

-- +goose Up
CREATE TABLE project_creators (
    project_id         TEXT PRIMARY KEY,
    creator_project_id TEXT NOT NULL,
    created_at         TIMESTAMP NOT NULL
);

-- +goose Down
DROP TABLE project_creators;
