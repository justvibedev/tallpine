-- +goose Up
-- On by default (Settings, General): coalesce repeated activity writes and
-- compact the raw provider-event archive. Off restores the previous write path
-- and stops the archive janitor; nothing already compacted comes back.
ALTER TABLE app_settings ADD COLUMN storage_compaction INTEGER NOT NULL DEFAULT 1
    CHECK (storage_compaction IN (0, 1));

-- Provider events of sessions terminated longer ago than this are deleted.
ALTER TABLE app_settings ADD COLUMN provider_event_retention_days INTEGER NOT NULL DEFAULT 14
    CHECK (provider_event_retention_days BETWEEN 1 AND 3650);

-- How far the archive janitor has compacted each conversation's provider
-- events. Rows above the watermark are still to be looked at; a delta of a turn
-- that has not settled yet holds the watermark below itself. A missing row
-- means nothing has been compacted, so an upgrade needs no backfill.
CREATE TABLE conversation_provider_event_compaction (
    conversation_id      TEXT PRIMARY KEY REFERENCES conversations(id) ON DELETE CASCADE,
    compacted_through_id INTEGER NOT NULL DEFAULT 0,
    updated_at           TIMESTAMP NOT NULL
);

-- How far the archive janitor has expired each terminated session's provider
-- events. Expiry deletes rows without a native identity and reduces the rest to
-- '{}' tombstones that keep their identity for replay dedupe, so a session that
-- is restored later cannot import its native history twice. Rows above the
-- cursor (a restored session's newer events) are expired when the session is
-- past retention again.
CREATE TABLE session_provider_event_retention (
    session_id         TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
    expired_through_id INTEGER NOT NULL DEFAULT 0,
    updated_at         TIMESTAMP NOT NULL
);

-- +goose Down
DROP TABLE session_provider_event_retention;
DROP TABLE conversation_provider_event_compaction;
ALTER TABLE app_settings DROP COLUMN provider_event_retention_days;
ALTER TABLE app_settings DROP COLUMN storage_compaction;
