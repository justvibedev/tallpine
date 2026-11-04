-- +goose Up

-- Every Chat controller start (spawn, restore, replacement, hibernation wake)
-- settles the work a previous controller left in flight, on the only writer
-- connection. Without these indexes that settle full-scanned
-- conversation_turns and the whole conversation_activities table: seconds on a
-- long history, stalling all streaming projection behind it.
--
-- Turns are found by the session that handled them, then filtered by state.
CREATE INDEX idx_conversation_turns_handled_by_session
    ON conversation_turns(handled_by_session_id, state);

-- Only running activities are ever settled by turn, and almost none are
-- running at any moment, so the partial index stays tiny and costs nothing on
-- the streaming write path once an activity finishes.
CREATE INDEX idx_conversation_activities_running_turn
    ON conversation_activities(turn_id)
    WHERE status = 'running';

-- +goose Down
DROP INDEX IF EXISTS idx_conversation_activities_running_turn;
DROP INDEX IF EXISTS idx_conversation_turns_handled_by_session;
