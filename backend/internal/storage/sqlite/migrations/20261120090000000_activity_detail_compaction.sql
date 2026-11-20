-- +goose Up

-- Progress of the storage janitor's one-time backfill that drops what activity
-- details repeat (B842): a command's output, when the command_output column
-- holds the streamed copy the read path shows instead, and a rawCommand equal
-- to command. Every row written after this migration is stored compacted, so
-- the backfill ends at the newest rowid that exists now.
CREATE TABLE activity_detail_compaction (
    id                      INTEGER PRIMARY KEY CHECK (id = 1),
    compacted_through_rowid INTEGER NOT NULL DEFAULT 0,
    through_rowid           INTEGER NOT NULL,
    updated_at              TIMESTAMP NOT NULL
);
INSERT INTO activity_detail_compaction (id, compacted_through_rowid, through_rowid, updated_at)
SELECT 1, 0, COALESCE(MAX(rowid), 0), CURRENT_TIMESTAMP FROM conversation_activities;

-- +goose Down
-- Rows already compacted stay compacted; the read path shows them the same.
DROP TABLE activity_detail_compaction;
