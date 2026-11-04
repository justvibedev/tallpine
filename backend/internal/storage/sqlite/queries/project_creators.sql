-- name: SetProjectCreator :execrows
INSERT INTO project_creators (project_id, creator_project_id, created_at) VALUES (?, ?, ?)
ON CONFLICT (project_id) DO NOTHING;

-- name: GetProjectCreator :one
SELECT creator_project_id FROM project_creators WHERE project_id = ?;

-- name: DeleteProjectCreator :exec
DELETE FROM project_creators WHERE project_id = ?;

-- name: ListAllProjectPaths :many
SELECT path FROM projects;
