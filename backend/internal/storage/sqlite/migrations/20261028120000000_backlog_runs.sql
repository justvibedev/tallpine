-- Summary: backlog run mode: durable run records and their per-item plan/status,
-- with backlog_changed CDC so the run card is live. change_log is untouched
-- (it has no event_type CHECK since the backlog CDC migration).
-- +goose Up
-- +goose StatementBegin
CREATE TABLE backlog_runs (
    id             TEXT PRIMARY KEY,
    project_id     TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    seq            INTEGER NOT NULL,
    scope          TEXT NOT NULL CHECK (scope IN ('local', 'pr', 'pr-merge')),
    selection_kind TEXT NOT NULL CHECK (selection_kind IN ('all', 'refs', 'parent')),
    selection      TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(selection) AND json_type(selection) = 'array'),
    status         TEXT NOT NULL CHECK (status IN ('planning', 'running', 'paused', 'stopping', 'done')),
    pause_reason   TEXT NOT NULL DEFAULT '' CHECK (length(pause_reason) <= 200),
    resume_at      TIMESTAMP,
    summary        TEXT NOT NULL DEFAULT '' CHECK (length(summary) <= 20000),
    requested_by   TEXT NOT NULL CHECK (requested_by IN ('human', 'orchestrator')),
    created_at     TIMESTAMP NOT NULL,
    updated_at     TIMESTAMP NOT NULL,
    started_at     TIMESTAMP,
    ended_at       TIMESTAMP,
    UNIQUE (project_id, seq),
    CHECK ((status = 'done') = (ended_at IS NOT NULL))
);
-- One open run per project.
CREATE UNIQUE INDEX idx_backlog_runs_open ON backlog_runs (project_id) WHERE status <> 'done';

CREATE TABLE backlog_run_items (
    run_id     TEXT NOT NULL REFERENCES backlog_runs(id) ON DELETE CASCADE,
    item_id    TEXT NOT NULL REFERENCES backlog_items(id) ON DELETE CASCADE,
    lane       INTEGER NOT NULL CHECK (lane >= 0),
    position   INTEGER NOT NULL CHECK (position >= 0),
    model      TEXT NOT NULL DEFAULT '' CHECK (length(model) <= 100),
    status     TEXT NOT NULL CHECK (status IN ('queued', 'active', 'in_review', 'needs_you', 'landed', 'held', 'failed')),
    reason     TEXT NOT NULL DEFAULT '' CHECK (length(reason) <= 500),
    question   TEXT NOT NULL DEFAULT '' CHECK (length(question) <= 1000),
    attempts   INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    updated_at TIMESTAMP NOT NULL,
    PRIMARY KEY (run_id, item_id)
);
CREATE INDEX idx_backlog_run_items_item ON backlog_run_items (item_id);

CREATE TRIGGER backlog_runs_cdc_insert
AFTER INSERT ON backlog_runs
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NULL, 'backlog_changed',
            json_object('id', NEW.id, 'ref', 'R' || NEW.seq, 'kind', 'run', 'state', NEW.status),
            CURRENT_TIMESTAMP);
END;

CREATE TRIGGER backlog_runs_cdc_update
AFTER UPDATE ON backlog_runs
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NULL, 'backlog_changed',
            json_object('id', NEW.id, 'ref', 'R' || NEW.seq, 'kind', 'run', 'state', NEW.status),
            CURRENT_TIMESTAMP);
END;

CREATE TRIGGER backlog_run_items_cdc_insert
AFTER INSERT ON backlog_run_items
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT r.project_id, NULL, 'backlog_changed',
           json_object('id', r.id, 'ref', 'R' || r.seq, 'kind', 'run', 'state', r.status),
           CURRENT_TIMESTAMP
    FROM backlog_runs r WHERE r.id = NEW.run_id;
END;

CREATE TRIGGER backlog_run_items_cdc_update
AFTER UPDATE ON backlog_run_items
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT r.project_id, NULL, 'backlog_changed',
           json_object('id', r.id, 'ref', 'R' || r.seq, 'kind', 'run', 'state', r.status),
           CURRENT_TIMESTAMP
    FROM backlog_runs r WHERE r.id = NEW.run_id;
END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER backlog_run_items_cdc_update;
DROP TRIGGER backlog_run_items_cdc_insert;
DROP TRIGGER backlog_runs_cdc_update;
DROP TRIGGER backlog_runs_cdc_insert;
DROP TABLE backlog_run_items;
DROP TABLE backlog_runs;
DELETE FROM change_log WHERE event_type = 'backlog_changed' AND json_extract(payload, '$.kind') = 'run';
-- +goose StatementEnd
