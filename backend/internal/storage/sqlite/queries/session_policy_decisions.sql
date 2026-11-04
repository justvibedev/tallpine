-- name: UpsertSessionPolicyDecision :exec
INSERT INTO session_policy_decisions (session_id, record, updated_at) VALUES (?, ?, ?)
ON CONFLICT (session_id) DO UPDATE SET record = excluded.record, updated_at = excluded.updated_at;

-- name: GetSessionPolicyDecision :one
SELECT record, updated_at FROM session_policy_decisions WHERE session_id = ?;
