-- +goose Up

-- Compaction boundaries, read by the archive janitor once per pass for every
-- conversation. A conversation compacts every few dozen turns, so this partial
-- index holds a few rows per conversation instead of scanning the archive.
CREATE INDEX idx_conversation_provider_events_compacted
    ON conversation_provider_events(conversation_id, id)
    WHERE method = 'thread.compacted';

-- How far the archive janitor has pruned each conversation's history below its
-- last settled compaction boundary. Compaction only shrinks the model's
-- context; without this the database keeps every provider event and every
-- command's full output for as long as the conversation lives (one long-lived
-- orchestrator conversation held 585k provider events and 21k activities).
--
-- boundary_event_id is the settled thread.compacted event the prune works
-- toward. events_pruned_through_id is the provider-event cursor below it (equal
-- to the boundary once done). activities_pruned_through_seq is the activity
-- cursor, and activities_done_boundary_id the boundary the activities were
-- finished for. A missing row means nothing has been pruned, so an upgrade
-- needs no backfill.
CREATE TABLE conversation_history_retention (
    conversation_id               TEXT PRIMARY KEY REFERENCES conversations(id) ON DELETE CASCADE,
    boundary_event_id             INTEGER NOT NULL DEFAULT 0,
    events_pruned_through_id      INTEGER NOT NULL DEFAULT 0,
    activities_pruned_through_seq INTEGER NOT NULL DEFAULT 0,
    activities_done_boundary_id   INTEGER NOT NULL DEFAULT 0,
    updated_at                    TIMESTAMP NOT NULL
);

-- +goose Down
DROP TABLE conversation_history_retention;
DROP INDEX IF EXISTS idx_conversation_provider_events_compacted;
