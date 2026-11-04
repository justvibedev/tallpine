-- name: UpsertAccountFailoverRetry :exec
INSERT INTO account_failover_retries (turn_id, session_id, move_id, moved, attempts, retry_at, created_at)
VALUES (sqlc.arg(turn_id), sqlc.arg(session_id), sqlc.arg(move_id), sqlc.arg(moved), sqlc.arg(attempts), sqlc.narg(retry_at), sqlc.arg(created_at))
ON CONFLICT (turn_id) DO UPDATE SET
    session_id = excluded.session_id,
    move_id = excluded.move_id,
    moved = excluded.moved,
    attempts = excluded.attempts,
    retry_at = excluded.retry_at;

-- name: DeleteAccountFailoverRetry :exec
DELETE FROM account_failover_retries WHERE turn_id = ?;

-- name: ListAccountFailoverRetries :many
SELECT turn_id, session_id, move_id, moved, attempts, retry_at, created_at
FROM account_failover_retries
ORDER BY created_at, turn_id;
