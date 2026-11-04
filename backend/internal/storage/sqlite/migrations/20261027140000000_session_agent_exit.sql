-- session_agent_exit records why a session's agent last exited, so startup and
-- sends resume only agents that went down involuntarily (their host died with
-- the daemon). A separate table keeps the sessions row shape unchanged; a
-- session without a row exited for an unknown reason and is never auto-resumed.

-- +goose Up
CREATE TABLE session_agent_exit (
    session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
    reason     TEXT NOT NULL
);

-- +goose Down
DROP TABLE session_agent_exit;
