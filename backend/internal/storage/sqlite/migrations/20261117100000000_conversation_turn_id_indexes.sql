-- +goose Up

-- SelectConversationTurns and SelectConversationTurnsPage keep a turn on a
-- forked lineage only when one of its messages or activities sits at or before
-- the fork point, correlated on turn_id. Without a turn_id index that EXISTS
-- range-scanned (conversation_id, sequence) once per turn: 29.8 s for a
-- 10-branch conversation of 6.2k turns, 0.019 s with these indexes. The
-- sequence column lets the "sequence <= fork point" test seek, not filter.
CREATE INDEX idx_conversation_messages_turn_sequence
    ON conversation_messages(turn_id, sequence);

CREATE INDEX idx_conversation_activities_turn_sequence
    ON conversation_activities(turn_id, sequence);

-- +goose Down
DROP INDEX IF EXISTS idx_conversation_activities_turn_sequence;
DROP INDEX IF EXISTS idx_conversation_messages_turn_sequence;
