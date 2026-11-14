-- Tracked owner asks (B698/B699). An orchestrator's question to the owner is a
-- daemon record owned by project + lane, not by a session, so it outlives a
-- restart and every orchestrator of the project sees it (dedupe). Each write
-- publishes on the backlog event stream (backlog_changed, kind "ask").

-- +goose Up
-- +goose StatementBegin
CREATE TABLE owner_asks (
    id                 TEXT PRIMARY KEY,
    project_id         TEXT NOT NULL,
    seq                INTEGER NOT NULL,
    lane               TEXT NOT NULL DEFAULT '',
    lane_ref           TEXT NOT NULL DEFAULT '',
    session_id         TEXT NOT NULL DEFAULT '',
    text               TEXT NOT NULL,
    reason             TEXT NOT NULL DEFAULT '',
    options_json       TEXT NOT NULL DEFAULT '[]',
    recommended        TEXT NOT NULL DEFAULT '',
    about              TEXT NOT NULL DEFAULT '',
    state              TEXT NOT NULL CHECK (state IN ('open', 'answered', 'withdrawn')),
    answer_json        TEXT NOT NULL DEFAULT '',
    reask_of           TEXT NOT NULL DEFAULT '',
    reask_reason       TEXT NOT NULL DEFAULT '',
    withdraw_reason    TEXT NOT NULL DEFAULT '',
    anchor_turn_id     TEXT NOT NULL DEFAULT '',
    anchor_activity_id TEXT NOT NULL DEFAULT '',
    created_at         TIMESTAMP NOT NULL,
    updated_at         TIMESTAMP NOT NULL,
    answered_at        TIMESTAMP
);
CREATE UNIQUE INDEX owner_asks_project_seq ON owner_asks (project_id, seq);
CREATE INDEX owner_asks_open ON owner_asks (project_id, lane) WHERE state = 'open';

CREATE TRIGGER owner_asks_cdc_insert
AFTER INSERT ON owner_asks
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NULL, 'backlog_changed',
            json_object('id', NEW.id, 'kind', 'ask', 'state', NEW.state), CURRENT_TIMESTAMP);
END;

CREATE TRIGGER owner_asks_cdc_update
AFTER UPDATE ON owner_asks
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NULL, 'backlog_changed',
            json_object('id', NEW.id, 'kind', 'ask', 'state', NEW.state), CURRENT_TIMESTAMP);
END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER owner_asks_cdc_update;
DROP TRIGGER owner_asks_cdc_insert;
DELETE FROM change_log WHERE event_type = 'backlog_changed' AND json_extract(payload, '$.kind') = 'ask';
DROP TABLE owner_asks;
-- +goose StatementEnd
