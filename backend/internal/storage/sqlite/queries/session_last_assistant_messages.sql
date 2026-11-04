-- name: UpsertSessionLastAssistantMessage :exec
-- A delayed turn end never replaces a newer one.
INSERT INTO session_last_assistant_messages (session_id, message, at)
VALUES (sqlc.arg(session_id), sqlc.arg(message), sqlc.arg(at))
ON CONFLICT (session_id) DO UPDATE SET message = excluded.message, at = excluded.at
WHERE excluded.at >= session_last_assistant_messages.at;

-- name: GetSessionLastAssistantMessage :one
SELECT message, at FROM session_last_assistant_messages WHERE session_id = sqlc.arg(session_id);
