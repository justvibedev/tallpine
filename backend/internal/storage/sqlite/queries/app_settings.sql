-- Daemon-owned user preferences. One row, seeded by migration 0042, so a read
-- never has to handle absence.

-- name: GetAppSettings :one
SELECT * FROM app_settings WHERE id = 1;

-- name: SetDefaultSessionMode :exec
UPDATE app_settings SET default_session_mode = ?, updated_at = ? WHERE id = 1;

-- name: SetDynamicOrchestratorContext :exec
UPDATE app_settings SET dynamic_orchestrator_context = ?, updated_at = ? WHERE id = 1;

-- name: SetVersionRefresh :exec
UPDATE app_settings SET version_refresh = ?, updated_at = ? WHERE id = 1;

-- name: SetHarnessDefault :exec
UPDATE app_settings
SET harness_defaults = json_set(harness_defaults, sqlc.arg(harness_path), json(sqlc.arg(config))),
    updated_at = sqlc.arg(updated_at)
WHERE id = 1;

-- name: ResetHarnessDefault :exec
UPDATE app_settings
SET harness_defaults = json_remove(harness_defaults, sqlc.arg(harness_path)),
    updated_at = sqlc.arg(updated_at)
WHERE id = 1;

-- name: SetReviewDefaults :exec
UPDATE app_settings SET review_defaults = ?, updated_at = ? WHERE id = 1;

-- name: SetStorageCompaction :exec
UPDATE app_settings
SET storage_compaction = sqlc.arg(storage_compaction),
    provider_event_retention_days = sqlc.arg(provider_event_retention_days),
    updated_at = sqlc.arg(updated_at)
WHERE id = 1;

-- name: SetChatHibernation :exec
UPDATE app_settings
SET chat_hibernation = sqlc.arg(enabled),
    chat_hibernation_worker_idle_seconds = sqlc.arg(worker_idle_seconds),
    chat_hibernation_orchestrator_idle_seconds = sqlc.arg(orchestrator_idle_seconds),
    updated_at = sqlc.arg(updated_at)
WHERE id = 1;

-- name: SetWorkflowDefaults :exec
UPDATE app_settings SET workflow_defaults = ?, updated_at = ? WHERE id = 1;
