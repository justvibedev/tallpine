-- +goose Up
-- A usage-limited turn whose session was moved to another account and still
-- needs its retry (B100). The move's commit disarms the turn's auto-resume, so
-- without this row a daemon restart between the commit and the retry would
-- lose the retry.
CREATE TABLE account_failover_retries (
    turn_id         TEXT PRIMARY KEY,
    session_id      TEXT NOT NULL,
    move_id         TEXT NOT NULL,
    moved           INTEGER NOT NULL DEFAULT 0,
    attempts        INTEGER NOT NULL DEFAULT 0,
    retry_at        DATETIME,
    created_at      DATETIME NOT NULL
);

-- +goose Down
DROP TABLE account_failover_retries;
