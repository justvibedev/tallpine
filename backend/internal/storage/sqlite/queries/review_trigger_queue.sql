-- name: EnqueueReviewTrigger :exec
INSERT INTO review_trigger_queue (session_id, pr_url, harness, created_at, model, effort, interface_mode, trigger_source, next_attempt_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (session_id, pr_url, harness) DO UPDATE SET
    trigger_source = excluded.trigger_source, model = excluded.model, effort = excluded.effort,
    interface_mode = excluded.interface_mode, created_at = excluded.created_at,
    attempts = 0, next_attempt_at = excluded.next_attempt_at
-- An explicit request takes over a parked automatic pass; nothing else replaces an entry.
WHERE review_trigger_queue.trigger_source = 'auto' AND excluded.trigger_source <> 'auto';

-- name: ListQueuedReviewTriggers :many
SELECT session_id, pr_url, harness, created_at, model, effort, interface_mode, trigger_source, attempts, next_attempt_at
FROM review_trigger_queue ORDER BY created_at ASC, session_id ASC, pr_url ASC;

-- name: RecordQueuedReviewTriggerAttempt :exec
UPDATE review_trigger_queue SET attempts = attempts + 1, next_attempt_at = ?
WHERE session_id = ? AND pr_url = ? AND harness = ?;

-- name: DeleteQueuedReviewTrigger :exec
DELETE FROM review_trigger_queue WHERE session_id = ? AND pr_url = ? AND harness = ?;
