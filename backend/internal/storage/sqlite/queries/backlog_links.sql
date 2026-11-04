-- name: InsertBacklogLink :execrows
INSERT INTO backlog_links (item_id, kind, ref, created_by, created_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (item_id, kind, ref) DO NOTHING;

-- name: DeleteBacklogLink :execrows
DELETE FROM backlog_links WHERE item_id = ? AND kind = ? AND ref = ?;

-- name: SelectBacklogLinks :many
SELECT * FROM backlog_links WHERE item_id = ? ORDER BY created_at, kind, ref;

-- name: SelectBacklogLinkCounts :many
SELECT item_id, kind, count(*) AS n FROM backlog_links
WHERE item_id IN (SELECT id FROM backlog_items WHERE project_id = ?)
GROUP BY item_id, kind;

-- name: SelectBacklogBlockers :many
SELECT ref FROM backlog_links WHERE item_id = ? AND kind = 'blocked_by';
