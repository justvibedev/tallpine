-- +goose Up

-- Identities of provider events removed from the archive (B832). The archive's
-- unique (conversation_id, provider_event_id) index is the projection's
-- idempotency gate: a replayed event whose identity is already archived is not
-- projected a second time. Compaction and retention used to keep a removed
-- row as a '{}' tombstone so that index still rejected a replay; those rows
-- and their four index entries were never deleted (1.56M of 2.1M archive rows
-- on one database). A removed row now leaves only its identity here, and an
-- insert is refused when its identity is in either place.
CREATE TABLE conversation_provider_event_keys (
    conversation_id   TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    provider_event_id TEXT NOT NULL,
    PRIMARY KEY (conversation_id, provider_event_id)
) WITHOUT ROWID;

-- Progress of the archive janitor's one-time sweep that turns the existing
-- tombstones into keys and deletes them. No tombstone is written after this
-- migration, so the sweep ends at the newest id that exists now.
CREATE TABLE provider_event_tombstone_sweep (
    id          INTEGER PRIMARY KEY CHECK (id = 1),
    swept_through_id INTEGER NOT NULL DEFAULT 0,
    through_id  INTEGER NOT NULL,
    updated_at  TIMESTAMP NOT NULL
);
INSERT INTO provider_event_tombstone_sweep (id, swept_through_id, through_id, updated_at)
SELECT 1, 0, COALESCE(MAX(id), 0), CURRENT_TIMESTAMP FROM conversation_provider_events;

-- The raw archive of a finished session is kept 7 days instead of 14. Nothing
-- reads an archived payload after it was projected; the window only serves
-- debugging a projection. 14 was the default and nobody could tell it from a
-- deliberate choice, so it is lowered; any other value was chosen and stays.
UPDATE app_settings SET provider_event_retention_days = 7 WHERE provider_event_retention_days = 14;

-- +goose Down
-- Rows removed since this migration ran are gone, not tombstoned, so after a
-- rollback their identities no longer refuse a replay.
UPDATE app_settings SET provider_event_retention_days = 14 WHERE provider_event_retention_days = 7;
DROP TABLE provider_event_tombstone_sweep;
DROP TABLE conversation_provider_event_keys;
