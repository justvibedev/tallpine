-- Summary: backlog_changed CDC. Rebuilds change_log without its event_type CHECK
-- (a CHECK failure inside a CDC trigger aborts the user's own write; the event
-- vocabulary is guarded by TestTriggerEventTypesMatchGoList instead) and adds
-- the backlog_items triggers. No other trigger is dropped or recreated:
-- legacy_alter_table keeps the rename from rewriting or rejecting the
-- triggers that reference change_log, and sqlite_master stays byte-identical
-- for everything but change_log (TestBacklogCDCMigrationPreservesSchema).
-- AUTOINCREMENT state lives in sqlite_sequence and dies with the dropped table,
-- so it is saved and restored; otherwise seq could restart below the retained
-- window and clients resuming by Last-Event-ID would skip events.
-- +goose Up
-- +goose StatementBegin
PRAGMA legacy_alter_table = ON;

CREATE TEMP TABLE change_log_seq_save AS
SELECT seq FROM sqlite_sequence WHERE name = 'change_log';

CREATE TABLE change_log_new (
    seq        INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id TEXT REFERENCES projects (id),
    session_id TEXT REFERENCES sessions (id),
    event_type TEXT NOT NULL,
    payload    TEXT NOT NULL CHECK (json_valid(payload)),
    created_at TIMESTAMP NOT NULL DEFAULT (datetime('now'))
);

INSERT INTO change_log_new (seq, project_id, session_id, event_type, payload, created_at)
SELECT seq, project_id, session_id, event_type, payload, created_at
FROM change_log;

DROP INDEX idx_change_log_project;
DROP INDEX idx_change_log_created_at_seq;
DROP TABLE change_log;
ALTER TABLE change_log_new RENAME TO change_log;
CREATE INDEX idx_change_log_project ON change_log (project_id, seq);
CREATE INDEX idx_change_log_created_at_seq ON change_log (created_at, seq);

UPDATE sqlite_sequence
SET seq = MAX(seq, COALESCE((SELECT seq FROM change_log_seq_save), 0))
WHERE name = 'change_log';
DROP TABLE change_log_seq_save;

PRAGMA legacy_alter_table = OFF;

CREATE TRIGGER backlog_items_cdc_insert
AFTER INSERT ON backlog_items
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NULL, 'backlog_changed',
            json_object('id', NEW.id, 'ref', 'B' || NEW.seq, 'state', NEW.state),
            CURRENT_TIMESTAMP);
END;

CREATE TRIGGER backlog_items_cdc_update
AFTER UPDATE ON backlog_items
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NULL, 'backlog_changed',
            json_object('id', NEW.id, 'ref', 'B' || NEW.seq, 'state', NEW.state),
            CURRENT_TIMESTAMP);
END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER backlog_items_cdc_update;
DROP TRIGGER backlog_items_cdc_insert;
DELETE FROM change_log WHERE event_type = 'backlog_changed';

PRAGMA legacy_alter_table = ON;

CREATE TEMP TABLE change_log_seq_save AS
SELECT seq FROM sqlite_sequence WHERE name = 'change_log';

CREATE TABLE change_log_new (
    seq        INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id TEXT REFERENCES projects (id),
    session_id TEXT REFERENCES sessions (id),
    event_type TEXT NOT NULL
        CHECK (event_type IN (
            'session_created',
            'session_updated',
            'pr_created',
            'pr_updated',
            'pr_check_recorded',
            'pr_session_changed',
            'pr_review_thread_added',
            'pr_review_thread_resolved',
            'review_run_created',
            'review_run_updated'
        )),
    payload    TEXT NOT NULL CHECK (json_valid(payload)),
    created_at TIMESTAMP NOT NULL DEFAULT (datetime('now'))
);

INSERT INTO change_log_new (seq, project_id, session_id, event_type, payload, created_at)
SELECT seq, project_id, session_id, event_type, payload, created_at
FROM change_log;

DROP INDEX idx_change_log_project;
DROP INDEX idx_change_log_created_at_seq;
DROP TABLE change_log;
ALTER TABLE change_log_new RENAME TO change_log;
CREATE INDEX idx_change_log_project ON change_log (project_id, seq);
CREATE INDEX idx_change_log_created_at_seq ON change_log (created_at, seq);

UPDATE sqlite_sequence
SET seq = MAX(seq, COALESCE((SELECT seq FROM change_log_seq_save), 0))
WHERE name = 'change_log';
DROP TABLE change_log_seq_save;

PRAGMA legacy_alter_table = OFF;
-- +goose StatementEnd
