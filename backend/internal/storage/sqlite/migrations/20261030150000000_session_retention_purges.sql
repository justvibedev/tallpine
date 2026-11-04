-- Session retention (B161): what the retention policy purged from a terminated
-- session. Restore refuses a session whose raw provider events or transcript
-- were purged: a native-history replay without the dedupe identities would
-- append its text a second time, and the timeline would be gone anyway.

-- +goose Up
CREATE TABLE session_retention_purges (
    session_id      TEXT PRIMARY KEY,
    purged_at       TIMESTAMP NOT NULL,
    provider_events INTEGER NOT NULL DEFAULT 0 CHECK (provider_events IN (0, 1)),
    transcripts     INTEGER NOT NULL DEFAULT 0 CHECK (transcripts IN (0, 1))
);

-- +goose Down
DROP TABLE session_retention_purges;
