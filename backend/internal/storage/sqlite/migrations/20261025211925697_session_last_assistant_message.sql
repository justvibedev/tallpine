-- +goose Up

-- The final assistant message of a session's latest main turn, for display
-- (session detail, `tallpine session get`). Kept apart from the sessions row: a
-- write on every turn end must not bump the row's revision (an activity CAS
-- token) or fan out session CDC, and it is never a replay checkpoint, unlike
-- sessions.latest_assistant_update.
CREATE TABLE session_last_assistant_messages (
    session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
    message TEXT NOT NULL,
    at TIMESTAMP NOT NULL
);

-- +goose Down
DROP TABLE session_last_assistant_messages;
