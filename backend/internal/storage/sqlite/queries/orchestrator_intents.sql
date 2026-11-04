-- Durable owner intents and standing decisions of a project's orchestrator.
-- done_at is set when an intent is cleared; open intents have done_at NULL.

-- name: InsertOrchestratorIntent :exec
INSERT INTO orchestrator_intents (id, project_id, text, created_by, created_at)
VALUES (?, ?, ?, ?, ?);

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
