-- Review passes an agent asked for while the worker's reviewer was busy, plus
-- reruns the one-shot retry could not start because the reviewer was busy. The
-- sweep starts each one once its reviewer is free, so none is dropped.
-- +goose Up
CREATE TABLE review_trigger_queue (
    session_id      TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    pr_url          TEXT NOT NULL,
    harness         TEXT NOT NULL,
    created_at      DATETIME NOT NULL,
    model           TEXT NOT NULL DEFAULT '',
    effort          TEXT NOT NULL DEFAULT '',
    interface_mode  TEXT NOT NULL DEFAULT '',
    trigger_source  TEXT NOT NULL DEFAULT 'agent',
    attempts        INTEGER NOT NULL DEFAULT 0,
    next_attempt_at DATETIME NOT NULL,
    PRIMARY KEY (session_id, pr_url, harness)
);

-- +goose Down
DROP TABLE review_trigger_queue;
