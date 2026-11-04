-- Per-project backlog items. Hierarchy, link and ordering rules are enforced
-- by the store inside one write transaction; the schema holds the CHECKs.

-- name: InsertBacklogItem :exec
INSERT INTO backlog_items (
    id, project_id, seq, title, note, done_when, state, hold, scope, parent_id, position,
    origin_kind, origin_session_id, origin_excerpt, agent_notes, fields, closed_reason,
    created_at, updated_at, closed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: SelectBacklogItemByID :one
SELECT * FROM backlog_items WHERE id = ?;

-- name: SelectBacklogItemByRef :one
SELECT * FROM backlog_items WHERE project_id = ? AND seq = ?;

-- name: SelectBacklogItemsByProject :many
SELECT * FROM backlog_items
WHERE project_id = sqlc.arg(project_id)
  AND (sqlc.arg(state) = '' OR state = sqlc.arg(state) OR (sqlc.arg(state) = 'active' AND state IN ('inbox', 'open')))
  AND (sqlc.arg(parent_id) = '' OR parent_id = sqlc.arg(parent_id))
  AND (sqlc.arg(held) = '' OR (sqlc.arg(held) = 'held' AND hold <> '') OR (sqlc.arg(held) = 'unheld' AND hold = ''))
  AND (sqlc.arg(query) = '' OR instr(lower(title), lower(sqlc.arg(query))) > 0)
ORDER BY position, seq
LIMIT sqlc.arg(row_limit);

-- name: UpdateBacklogItem :execrows
UPDATE backlog_items
SET title = ?, note = ?, done_when = ?, hold = ?, scope = ?, parent_id = ?,
    agent_notes = ?, fields = ?, updated_at = ?
WHERE id = ?;

-- name: UpdateBacklogItemState :execrows
UPDATE backlog_items
SET state = ?, closed_at = ?, closed_reason = ?, updated_at = ?
WHERE id = ?;

-- name: UpdateBacklogItemPosition :exec
UPDATE backlog_items SET position = ?, updated_at = ? WHERE id = ?;

-- name: TouchBacklogItem :exec
UPDATE backlog_items SET updated_at = ? WHERE id = ?;

-- name: CountBacklogChildren :one
SELECT count(*) FROM backlog_items WHERE parent_id = ?;

-- name: CountBacklogChildrenClosed :one
SELECT count(*) FROM backlog_items WHERE parent_id = ? AND state IN ('done', 'dropped');

-- name: CountBacklogChildStatsByProject :many
SELECT parent_id,
       count(*) AS total,
       CAST(COALESCE(sum(CASE WHEN state IN ('done', 'dropped') THEN 1 ELSE 0 END), 0) AS INTEGER) AS closed
FROM backlog_items
WHERE project_id = ? AND parent_id IS NOT NULL
GROUP BY parent_id;

-- name: SelectBacklogChildren :many
SELECT * FROM backlog_items WHERE parent_id = ? ORDER BY position, seq;

-- name: SelectBacklogPositions :many
SELECT id, position FROM backlog_items WHERE project_id = ? ORDER BY position, seq;

-- name: SelectBacklogMaxPosition :one
SELECT CAST(COALESCE(MAX(position), 0) AS INTEGER) FROM backlog_items WHERE project_id = ?;

-- name: SelectBacklogLiveItems :many
SELECT * FROM backlog_items
WHERE project_id = ? AND state IN ('inbox', 'open') ORDER BY seq;

-- name: CountBacklogItemsByOriginSession :one
SELECT count(*) FROM backlog_items
WHERE project_id = ? AND origin_kind = 'worker' AND origin_session_id = ?;

-- name: CountBacklogInbox :one
SELECT count(*) FROM backlog_items WHERE project_id = ? AND state = 'inbox';

-- name: CountBacklogInboxByProject :many
SELECT i.project_id, count(*) AS inbox
FROM backlog_items i
JOIN projects p ON p.id = i.project_id
WHERE i.state = 'inbox' AND p.archived_at IS NULL
GROUP BY i.project_id;

-- name: SelectNextBacklogItem :one
SELECT i.* FROM backlog_items i
WHERE i.project_id = sqlc.arg(project_id)
  AND i.state = 'open'
  AND i.hold = ''
  AND NOT EXISTS (SELECT 1 FROM backlog_items c WHERE c.parent_id = i.id)
  AND NOT EXISTS (
      SELECT 1 FROM backlog_links l
      JOIN backlog_items b ON b.id = l.ref
      WHERE l.item_id = i.id AND l.kind = 'blocked_by' AND b.state IN ('inbox', 'open')
  )
  AND (sqlc.arg(parent_id) = '' OR i.parent_id = sqlc.arg(parent_id))
ORDER BY i.position, i.seq
LIMIT 1;

-- name: DeleteBacklogItem :execrows
DELETE FROM backlog_items WHERE id = ?;

-- name: SelectClosedBacklogForPurge :many
-- Closed items of a project, oldest first. state_done / state_dropped select
-- which closed states count; the age filter is applied in Go.
SELECT * FROM backlog_items
WHERE project_id = sqlc.arg(project_id)
  AND ((sqlc.arg(state_done) = 1 AND state = 'done') OR (sqlc.arg(state_dropped) = 1 AND state = 'dropped'))
ORDER BY seq;

-- name: InsertBacklogChangeLog :exec
-- Hand-emitted backlog_changed row for a deletion. An AFTER DELETE trigger would
-- fire for the cascade of a project delete and violate change_log's project FK.
INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
VALUES (?, NULL, 'backlog_changed', json_object('id', sqlc.arg(item_id), 'ref', sqlc.arg(ref), 'state', sqlc.arg(state)), CURRENT_TIMESTAMP);
