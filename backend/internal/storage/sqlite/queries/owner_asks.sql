-- name: InsertOwnerAsk :exec
INSERT INTO owner_asks (
    id, project_id, seq, lane, lane_ref, session_id, text, reason, options_json, recommended, about,
    state, answer_json, reask_of, reask_reason, withdraw_reason, anchor_turn_id, anchor_activity_id,
    created_at, updated_at, answered_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: NextOwnerAskSeq :one
SELECT CAST(COALESCE(MAX(seq), 0) + 1 AS INTEGER) AS seq FROM owner_asks WHERE project_id = ?;

-- name: GetOwnerAskByRef :one
SELECT * FROM owner_asks WHERE project_id = ? AND seq = ?;

-- name: GetOwnerAskByID :one
SELECT * FROM owner_asks WHERE id = ?;

-- name: ListOwnerAsks :many
SELECT * FROM owner_asks WHERE project_id = ? ORDER BY seq;

-- name: ListOpenOwnerAsks :many
SELECT * FROM owner_asks WHERE project_id = ? AND state = 'open' ORDER BY seq;

-- name: UpdateOwnerAskIfOpen :execrows
UPDATE owner_asks SET
    state = ?, answer_json = ?, withdraw_reason = ?, updated_at = ?, answered_at = ?
WHERE id = ? AND state = 'open';

-- name: SetOwnerAskAnchor :exec
UPDATE owner_asks SET anchor_turn_id = ?, anchor_activity_id = ? WHERE id = ?;
