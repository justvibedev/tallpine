-- name: GetSessionLaunchOptions :one
SELECT launch_options FROM sessions WHERE id = ?;

-- name: SetSessionLaunchOptions :exec
UPDATE sessions SET revision = revision + 1, launch_options = ? WHERE id = ?;
