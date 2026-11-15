-- Durable owner intents and standing decisions of a project's orchestrator.
-- done_at is set when an intent is cleared; open intents have done_at NULL.

-- name: InsertOrchestratorIntent :exec
INSERT INTO orchestrator_intents (id, project_id, text, created_by, created_at, wake_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: SelectOrchestratorIntents :many
SELECT *
FROM orchestrator_intents
WHERE project_id = ?
  AND (CAST(sqlc.arg(include_done) AS INTEGER) = 1 OR done_at IS NULL)
ORDER BY created_at, id;

-- name: MarkOrchestratorIntentDone :execrows
UPDATE orchestrator_intents
SET done_at = ?
WHERE id = ? AND project_id = ? AND done_at IS NULL;

-- name: SelectDueIntentWakes :many
-- Open intents whose wake time has passed and whose one wake is not yet
-- delivered (or claimed).
SELECT *
FROM orchestrator_intents
WHERE done_at IS NULL AND wake_at IS NOT NULL AND woken_at IS NULL AND wake_at <= ?
ORDER BY wake_at, id;

-- name: ClaimIntentWake :execrows
-- Claims the intent's single wake; zero rows means it was cleared or already claimed.
UPDATE orchestrator_intents
SET woken_at = ?
WHERE id = ? AND done_at IS NULL AND woken_at IS NULL;

-- name: ReleaseIntentWake :exec
-- Gives a claimed wake back after a failed delivery so the next sweep retries.
UPDATE orchestrator_intents
SET woken_at = NULL
WHERE id = ? AND done_at IS NULL;
