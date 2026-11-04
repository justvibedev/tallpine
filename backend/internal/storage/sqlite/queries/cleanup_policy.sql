-- Automatic cleanup policy and run history. The policy row is seeded by
-- migration, so a read never has to handle absence.

-- name: GetCleanupPolicy :one
SELECT policy, updated_at FROM cleanup_settings WHERE id = 1;

-- name: SetCleanupPolicy :exec
UPDATE cleanup_settings SET policy = ?, updated_at = ? WHERE id = 1;

-- name: InsertCleanupRun :one
INSERT INTO cleanup_runs (started_at, finished_at, trigger, pass, dry_run, freed_bytes, report, error)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: ListCleanupRuns :many
SELECT * FROM cleanup_runs ORDER BY id DESC LIMIT ?;

-- name: LatestCleanupRun :many
SELECT * FROM cleanup_runs WHERE dry_run = ? ORDER BY id DESC LIMIT 1;

-- name: CleanupRunTrimCutoff :many
SELECT id FROM cleanup_runs WHERE dry_run = ? ORDER BY id DESC LIMIT 1 OFFSET ?;

-- name: DeleteCleanupRunsUpTo :exec
DELETE FROM cleanup_runs WHERE dry_run = ? AND id <= ?;
