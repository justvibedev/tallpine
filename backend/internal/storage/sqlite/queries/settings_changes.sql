-- name: InsertSettingsChange :exec
INSERT INTO settings_changes (changed_at, actor, project_id, setting_key, old_value, new_value)
VALUES (?, ?, ?, ?, ?, ?);

-- name: ListSettingsChanges :many
SELECT id, changed_at, actor, project_id, setting_key, old_value, new_value
FROM settings_changes
WHERE project_id = ?
ORDER BY id DESC
LIMIT ?;
