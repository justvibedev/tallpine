-- Planner role and plan records (B205 PR 1). backlog_plans holds the plan
-- before build for one backlog item: questions, assumptions, answers, the plan
-- text, decisions table and PR split. The database is the source of truth, not
-- the planner session's artifact directory (cleanup may remove that). A
-- planner session is a worker with sessions.role = 'planner'.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN role TEXT NOT NULL DEFAULT '';

CREATE TABLE backlog_plans (
    id                    TEXT PRIMARY KEY,
    project_id            TEXT NOT NULL,
    item_id               TEXT NOT NULL REFERENCES backlog_items(id) ON DELETE CASCADE,
    status                TEXT NOT NULL,
    planner_session_id    TEXT NOT NULL DEFAULT '',
    base_commit           TEXT NOT NULL DEFAULT '',
    base_branch           TEXT NOT NULL DEFAULT '',
    questions_json        TEXT NOT NULL DEFAULT '[]',
    assumptions_json      TEXT NOT NULL DEFAULT '[]',
    answers_json          TEXT NOT NULL DEFAULT '',
    plan_markdown         TEXT NOT NULL DEFAULT '',
    decisions_markdown    TEXT NOT NULL DEFAULT '',
    pr_split_json         TEXT NOT NULL DEFAULT '[]',
    review_focus_json     TEXT NOT NULL DEFAULT '[]',
    approved_by           TEXT NOT NULL DEFAULT '',
    approved_at           TIMESTAMP,
    owner_message_id      TEXT NOT NULL DEFAULT '',
    build_anyway          INTEGER NOT NULL DEFAULT 0,
    item_updated_at_start TIMESTAMP NOT NULL,
    started_at            TIMESTAMP NOT NULL,
    updated_at            TIMESTAMP NOT NULL,
    failure               TEXT NOT NULL DEFAULT ''
);
CREATE INDEX backlog_plans_item ON backlog_plans (item_id, started_at);
CREATE INDEX backlog_plans_planner ON backlog_plans (planner_session_id) WHERE planner_session_id <> '';
CREATE UNIQUE INDEX backlog_plans_one_open ON backlog_plans (item_id)
    WHERE status NOT IN ('approved', 'superseded', 'failed');

-- Each plan write publishes on the backlog event stream (backlog_changed), so
-- clients learn of awaiting_answers / awaiting_approval without polling.
CREATE TRIGGER backlog_plans_cdc_insert
AFTER INSERT ON backlog_plans
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NULL, 'backlog_changed',
            json_object('id', NEW.id, 'item_id', NEW.item_id, 'kind', 'plan', 'state', NEW.status),
            CURRENT_TIMESTAMP);
END;

CREATE TRIGGER backlog_plans_cdc_update
AFTER UPDATE ON backlog_plans
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NULL, 'backlog_changed',
            json_object('id', NEW.id, 'item_id', NEW.item_id, 'kind', 'plan', 'state', NEW.status),
            CURRENT_TIMESTAMP);
END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER backlog_plans_cdc_update;
DROP TRIGGER backlog_plans_cdc_insert;
DELETE FROM change_log WHERE event_type = 'backlog_changed' AND json_extract(payload, '$.kind') = 'plan';
DROP TABLE backlog_plans;
ALTER TABLE sessions DROP COLUMN role;
-- +goose StatementEnd
