-- Backlog run mode: one durable record per "finish the backlog" run and the
-- orchestrator's per-item plan/status. Rules live in the service; these are
-- plain reads and writes.

-- name: SelectNextBacklogRunSeq :one
SELECT CAST(COALESCE(MAX(seq), 0) + 1 AS INTEGER) FROM backlog_runs WHERE project_id = ?;

-- name: InsertBacklogRun :exec
INSERT INTO backlog_runs (
    id, project_id, seq, scope, selection_kind, selection, status, pause_reason, resume_at,
    summary, requested_by, created_at, updated_at, started_at, ended_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: SelectBacklogRunBySeq :one
SELECT * FROM backlog_runs WHERE project_id = ? AND seq = ?;

-- name: SelectBacklogRunByID :one
SELECT * FROM backlog_runs WHERE id = ?;

-- name: SelectBacklogRuns :many
SELECT * FROM backlog_runs
WHERE project_id = sqlc.arg(project_id) AND (sqlc.arg(open_only) = 0 OR status <> 'done')
ORDER BY seq DESC
LIMIT sqlc.arg(row_limit);

-- name: UpdateBacklogRun :exec
UPDATE backlog_runs
SET status = ?, pause_reason = ?, resume_at = ?, summary = ?, updated_at = ?, started_at = ?, ended_at = ?
WHERE id = ?;

-- name: SelectBacklogRunItems :many
SELECT ri.run_id, ri.item_id, ri.lane, ri.position, ri.model, ri.status, ri.reason, ri.question,
       ri.attempts, ri.updated_at,
       i.seq AS item_seq, i.title AS item_title, i.state AS item_state, i.hold AS item_hold
FROM backlog_run_items ri
JOIN backlog_items i ON i.id = ri.item_id
WHERE ri.run_id = ?
ORDER BY ri.lane, ri.position;

-- name: InsertBacklogRunItem :exec
INSERT INTO backlog_run_items (run_id, item_id, lane, position, model, status, reason, question, attempts, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpdateBacklogRunItem :exec
UPDATE backlog_run_items
SET lane = ?, position = ?, model = ?, status = ?, reason = ?, question = ?, attempts = ?, updated_at = ?
WHERE run_id = ? AND item_id = ?;

-- name: DeleteBacklogRunItem :exec
DELETE FROM backlog_run_items WHERE run_id = ? AND item_id = ?;

-- name: CountBacklogRunActiveInProject :one
-- Items currently holding a worker slot in any open run of the project: stored
-- active (in_review frees the slot) and not already closed by the lifecycle.
SELECT CAST(COUNT(*) AS INTEGER)
FROM backlog_run_items ri
JOIN backlog_runs r ON r.id = ri.run_id
JOIN backlog_items i ON i.id = ri.item_id
WHERE r.project_id = ? AND r.status <> 'done' AND ri.status = 'active'
  AND i.state NOT IN ('done', 'dropped');

-- name: SelectBacklogItemUsageCost :one
-- Cost of the sessions linked to an item, from the canonical usage ledger.
-- estimated_cost_nanos is NULL for an unpriced event, so priced_events < events
-- marks the total as partial.
SELECT
    CAST(COALESCE(SUM(mue.estimated_cost_nanos), 0) AS INTEGER) AS priced_nanos,
    CAST(COUNT(mue.id) AS INTEGER) AS events,
    CAST(COUNT(mue.estimated_cost_nanos) AS INTEGER) AS priced_events
FROM backlog_links l
JOIN sessions s ON s.id = l.ref AND s.kind <> 'orchestrator'
JOIN usage_bindings ub ON ub.session_id = s.id
JOIN model_usage_events mue ON mue.binding_id = ub.id
WHERE l.item_id = ? AND l.kind = 'session';

-- name: SelectBacklogRunUsageCost :one
-- Cost of a run: every distinct worker session linked to any of its items, so
-- a session shared by two items counts once.
SELECT
    CAST(COALESCE(SUM(mue.estimated_cost_nanos), 0) AS INTEGER) AS priced_nanos,
    CAST(COUNT(mue.id) AS INTEGER) AS events,
    CAST(COUNT(mue.estimated_cost_nanos) AS INTEGER) AS priced_events
FROM sessions s
JOIN usage_bindings ub ON ub.session_id = s.id
JOIN model_usage_events mue ON mue.binding_id = ub.id
WHERE s.kind <> 'orchestrator' AND s.id IN (
    SELECT l.ref FROM backlog_links l
    JOIN backlog_run_items ri ON ri.item_id = l.item_id
    WHERE ri.run_id = ? AND l.kind = 'session'
);

-- name: SelectBacklogItemFirstSession :one
-- When the earliest session linked to the item started (no rows: none linked).
SELECT s.created_at
FROM backlog_links l
JOIN sessions s ON s.id = l.ref
WHERE l.item_id = ? AND l.kind = 'session'
ORDER BY s.created_at
LIMIT 1;
