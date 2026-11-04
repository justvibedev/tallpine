-- name: SetSessionLane :exec
INSERT INTO session_lane (session_id, lane_item_id, parent_session_id) VALUES (?, ?, ?)
ON CONFLICT (session_id) DO UPDATE SET
    lane_item_id = excluded.lane_item_id, parent_session_id = excluded.parent_session_id;

-- name: GetSessionLane :one
SELECT lane_item_id, parent_session_id FROM session_lane WHERE session_id = ?;

-- name: ListSessionLanesByProject :many
SELECT l.session_id, l.lane_item_id, l.parent_session_id
FROM session_lane l JOIN sessions s ON s.id = l.session_id
WHERE s.project_id = ?;

-- name: ListPendingReportLanes :many
SELECT DISTINCT lane_item_id FROM reports
WHERE project_id = ? AND delivery_state = 'pending' AND available_at <= ?;

-- name: SelectBacklogSeqsByIDs :many
SELECT id, seq FROM backlog_items WHERE id IN (sqlc.slice(ids));
