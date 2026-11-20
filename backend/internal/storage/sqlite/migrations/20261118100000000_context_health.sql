-- Context health (B824/B825): persisted findings about what every session of a
-- project loads on start, the daily run marker, and the origin of an owner ask.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE context_health_findings (
    id              TEXT PRIMARY KEY,
    project_id      TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    kind            TEXT NOT NULL,
    fingerprint     TEXT NOT NULL,
    state           TEXT NOT NULL CHECK (state IN ('queued', 'open', 'accepted', 'declined', 'resolved')),
    evidence        TEXT NOT NULL DEFAULT '',
    measured_tokens INTEGER NOT NULL DEFAULT 0,
    budget_tokens   INTEGER NOT NULL DEFAULT 0,
    artifact_dir    TEXT NOT NULL DEFAULT '',
    ask_id          TEXT NOT NULL DEFAULT '',
    ask_ref         TEXT NOT NULL DEFAULT '',
    backlog_item_id TEXT NOT NULL DEFAULT '',
    decline_reason  TEXT NOT NULL DEFAULT '',
    declined_at     TIMESTAMP,
    declined_tokens INTEGER NOT NULL DEFAULT 0,
    created_at      TIMESTAMP NOT NULL,
    updated_at      TIMESTAMP NOT NULL,
    UNIQUE (project_id, fingerprint)
);
CREATE INDEX context_health_findings_ask ON context_health_findings (ask_id) WHERE ask_id <> '';

CREATE TABLE context_health_runs (
    project_id TEXT PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    ran_at     TIMESTAMP NOT NULL,
    snapshot   TEXT NOT NULL DEFAULT ''
);

-- The daemon, not an orchestrator, files some asks; their answers go to the
-- daemon's own handler instead of a chat. '' is an orchestrator ask.
ALTER TABLE owner_asks ADD COLUMN origin TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE owner_asks DROP COLUMN origin;
DROP TABLE context_health_runs;
DROP TABLE context_health_findings;
-- +goose StatementEnd
