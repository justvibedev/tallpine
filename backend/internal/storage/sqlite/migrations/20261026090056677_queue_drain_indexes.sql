-- +goose Up

-- After every turn the Chat controller drains its send queue
-- (SelectNextQueuedConversationTurn), joining the queued turn to its user
-- message by turn_id. With no turn_id index the planner scanned every
-- conversation_message ever recorded, a cost that grew with history.
--
-- Partial on role = 'user' on purpose: only the queue statements carry that
-- predicate. The lineage and page reads correlate on turn_id too, but they
-- are tuned to seek (conversation_id, sequence); a general turn_id index lets
-- the planner trade that for worse plans once sqlite_stat1 exists.
CREATE INDEX idx_conversation_messages_user_turn
    ON conversation_messages(turn_id)
    WHERE role = 'user';

-- Queued turns are rare and short-lived, so the queue head and the hibernation
-- wake sweep (ListHibernatedChatsWithQueuedTurns) read a near-empty index
-- instead of every turn.
CREATE INDEX idx_conversation_turns_queued
    ON conversation_turns(conversation_id, requested_at)
    WHERE state = 'queued';

-- +goose Down
DROP INDEX IF EXISTS idx_conversation_turns_queued;
DROP INDEX IF EXISTS idx_conversation_messages_user_turn;
