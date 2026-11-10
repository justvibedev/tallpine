-- name: CreateReport :one
INSERT INTO reports (
    id, session_id, project_id, state, note, message, created_at, available_at,
    settlement_deadline, repeat_count, synthesized, lane_item_id
)
VALUES (
    sqlc.arg(id), sqlc.arg(session_id), sqlc.arg(project_id), sqlc.arg(state),
    sqlc.arg(note), sqlc.arg(message), sqlc.arg(created_at), sqlc.arg(available_at),
    sqlc.arg(settlement_deadline), sqlc.arg(repeat_count), sqlc.arg(synthesized),
    COALESCE((SELECT l.lane_item_id FROM session_lane l WHERE l.session_id = sqlc.arg(session_id)), '')
)
RETURNING *;

-- name: CreateReportOutput :exec
INSERT INTO report_outputs (report_id, position, kind, reference, label)
VALUES (?, ?, ?, ?, ?);

-- name: GetReport :one
SELECT * FROM reports WHERE id = ?;

-- name: ListReportOutputs :many
SELECT * FROM report_outputs WHERE report_id = ? ORDER BY position;

-- name: ListReportsBySession :many
SELECT * FROM reports WHERE session_id = ? ORDER BY created_at, id;

-- name: ListReportedPRURLs :many
SELECT o.reference FROM report_outputs o
JOIN reports r ON r.id = o.report_id
WHERE r.session_id = ? AND o.kind = 'pr_created'
ORDER BY r.created_at, r.id, o.position;

-- name: ListReportsByProject :many
SELECT * FROM reports WHERE project_id = ? ORDER BY created_at, id;

-- name: ReportBatchOpen :one
SELECT EXISTS (
    SELECT 1 FROM reports
    WHERE project_id = ? AND delivery_state IN ('pending', 'claimed') AND delivery_batch_id = ?
) AS open;

-- name: ListPendingReportSchedule :many
-- One row per project: that project's earliest pending report. A global
-- earliest-first list would let one undeliverable project starve the rest.
SELECT * FROM reports
WHERE id IN (
    SELECT ranked.id FROM (
        SELECT r.id, ROW_NUMBER() OVER (
            PARTITION BY r.project_id ORDER BY r.available_at, r.created_at, r.id
        ) AS rn
        FROM reports r
        WHERE r.delivery_state = 'pending'
    ) AS ranked
    WHERE ranked.rn = 1
)
ORDER BY available_at, created_at, id
LIMIT ?;

-- name: ListPendingReportsByProjectSchedule :many
SELECT * FROM reports
WHERE project_id = ? AND delivery_state = 'pending' AND lane_item_id = ?
ORDER BY created_at, id
LIMIT ?;

-- name: HoldReportsBehindRetry :execrows
-- Keeps authored order: while an older retry batch backs off until hold_until,
-- the lane's newer non-urgent reports wait for it instead of overtaking it. A
-- head backing off for a busy orchestrator passes its marker on, so expediting
-- the head (orchestrator idle again) releases them too.
UPDATE reports SET available_at = sqlc.arg(hold_until),
    last_error = CASE WHEN sqlc.arg(busy_reason) != '' THEN sqlc.arg(busy_reason) ELSE last_error END
WHERE project_id = sqlc.arg(project_id) AND delivery_state = 'pending'
  AND lane_item_id = sqlc.arg(lane_item_id) AND delivery_batch_id != sqlc.arg(batch_id)
  AND available_at < sqlc.arg(hold_until)
  AND state NOT IN ('needs_input', 'stuck') AND synthesized = 0;

-- name: UrgentReportsDue :one
SELECT COUNT(*) FROM reports
WHERE project_id = ? AND delivery_state = 'pending' AND lane_item_id = ?
  AND available_at <= ? AND (state IN ('needs_input', 'stuck') OR synthesized = 1);

-- name: AssignPendingReportsToBatch :exec
UPDATE reports
SET delivery_batch_id = sqlc.arg(delivery_batch_id)
WHERE project_id = sqlc.arg(project_id) AND delivery_state = 'pending'
  AND lane_item_id = sqlc.arg(lane_item_id) AND delivery_batch_id = '';

-- name: ClaimPendingReportsByBatch :many
UPDATE reports
SET delivery_state = 'claimed', claim_token = sqlc.arg(claim_token),
    claimed_at = sqlc.arg(claimed_at), delivery_attempts = delivery_attempts + 1,
    last_error = ''
WHERE project_id = sqlc.arg(project_id) AND delivery_state = 'pending'
  AND delivery_batch_id = sqlc.arg(delivery_batch_id)
RETURNING *;

-- name: SweepFreshLaneReportsIntoBatch :many
UPDATE reports
SET delivery_state = 'claimed', claim_token = sqlc.arg(claim_token),
    claimed_at = sqlc.arg(claimed_at), delivery_attempts = delivery_attempts + 1,
    last_error = '', delivery_batch_id = sqlc.arg(delivery_batch_id)
WHERE project_id = sqlc.arg(project_id) AND delivery_state = 'pending'
  AND lane_item_id = sqlc.arg(lane_item_id)
  AND delivery_batch_id = '' AND delivery_attempts = 0
RETURNING *;

-- name: AcknowledgeReportBatch :many
UPDATE reports
SET delivery_state = 'acknowledged', acknowledged_at = sqlc.arg(acknowledged_at)
WHERE project_id = sqlc.arg(project_id) AND delivery_state = 'claimed'
  AND claim_token = sqlc.arg(claim_token)
RETURNING *;

-- name: ReleaseReportBatch :many
UPDATE reports
SET delivery_state = 'pending', available_at = min(available_at, sqlc.arg(available_at)),
    claim_token = '', claimed_at = NULL, last_error = sqlc.arg(last_error)
WHERE project_id = sqlc.arg(project_id) AND delivery_state = 'claimed'
  AND claim_token = sqlc.arg(claim_token)
RETURNING *;

-- name: DeferReportBatch :many
UPDATE reports
SET delivery_state = 'pending', available_at = max(available_at, sqlc.arg(available_at)),
    claim_token = '', claimed_at = NULL, last_error = sqlc.arg(last_error)
WHERE project_id = sqlc.arg(project_id) AND delivery_state = 'claimed'
  AND claim_token = sqlc.arg(claim_token)
RETURNING *;

-- name: GetReportInterrupt :one
SELECT last_interrupted_at FROM report_worker_interrupts WHERE session_id = ?;

-- name: PutReportInterrupt :exec
INSERT INTO report_worker_interrupts (session_id, last_interrupted_at)
VALUES (?, ?)
ON CONFLICT(session_id) DO UPDATE SET last_interrupted_at = excluded.last_interrupted_at;

-- name: ListPendingReports :many
SELECT * FROM reports
WHERE delivery_state = 'pending' AND available_at <= ?
ORDER BY created_at, id
LIMIT ?;

-- name: ClaimReport :one
UPDATE reports
SET delivery_state = 'claimed', claim_token = sqlc.arg(claim_token),
    claimed_at = sqlc.arg(claimed_at), delivery_attempts = delivery_attempts + 1,
    last_error = ''
WHERE id = sqlc.arg(id) AND delivery_state = 'pending' AND available_at <= sqlc.arg(claimed_at)
RETURNING *;

-- name: AcknowledgeReport :one
UPDATE reports
SET delivery_state = 'acknowledged', acknowledged_at = sqlc.arg(acknowledged_at)
WHERE id = sqlc.arg(id) AND delivery_state = 'claimed' AND claim_token = sqlc.arg(claim_token)
RETURNING *;

-- name: ReleaseReport :one
UPDATE reports
SET delivery_state = 'pending', available_at = sqlc.arg(available_at),
    claim_token = '', claimed_at = NULL, last_error = sqlc.arg(last_error)
WHERE id = sqlc.arg(id) AND delivery_state = 'claimed' AND claim_token = sqlc.arg(claim_token)
RETURNING *;

-- name: RequeueClaimedReports :execrows
UPDATE reports
SET delivery_state = 'pending', claim_token = '', claimed_at = NULL,
    last_error = 'delivery claim recovered after daemon restart'
WHERE delivery_state = 'claimed';

-- name: DropClaimedReport :one
-- Settles one claimed report without delivering it. The dropped token names why
-- and last_error keeps the reason as the durable record.
UPDATE reports
SET delivery_state = 'acknowledged', claim_token = sqlc.arg(dropped_token),
    acknowledged_at = sqlc.arg(dropped_at), last_error = sqlc.arg(last_error)
WHERE id = sqlc.arg(id) AND delivery_state = 'claimed'
  AND claim_token = sqlc.arg(claim_token)
RETURNING *;

-- name: DropPendingProjectReports :many
-- Settles a project's pending reports without delivering them. The claim token
-- names why, and last_error keeps the reason as the durable record.
UPDATE reports
SET delivery_state = 'acknowledged', claim_token = sqlc.arg(claim_token),
    claimed_at = sqlc.arg(dropped_at), acknowledged_at = sqlc.arg(dropped_at),
    last_error = sqlc.arg(last_error)
WHERE project_id = sqlc.arg(project_id) AND delivery_state = 'pending'
RETURNING *;

-- name: ReleaseInitialReportDelays :exec
-- Upgrade old batching deadlines without changing retry/backoff or batch identity.
UPDATE reports
SET available_at = created_at
WHERE delivery_state = 'pending' AND delivery_attempts = 0
  AND delivery_batch_id = '' AND available_at > created_at
  AND id NOT IN (SELECT report_id FROM report_quiet);

-- name: MarkReportQuiet :exec
INSERT INTO report_quiet (report_id) VALUES (?);

-- name: ExpediteBusyReports :exec
UPDATE reports SET available_at = sqlc.arg(available_at)
WHERE project_id = sqlc.arg(project_id) AND delivery_state = 'pending'
  AND last_error = sqlc.arg(last_error) AND available_at > sqlc.arg(available_at);

-- name: FlushQuietReports :execrows
-- Releases a project's waiting quiet reports once at least min_count of them
-- queued (the digest count threshold). Retry backoff of failed batches is
-- untouched: only never-attempted reports are released.
UPDATE reports SET available_at = sqlc.arg(available_at)
WHERE reports.project_id = sqlc.arg(project_id) AND reports.delivery_state = 'pending'
  AND reports.delivery_attempts = 0 AND reports.available_at > sqlc.arg(available_at)
  AND reports.id IN (SELECT report_id FROM report_quiet)
  AND (SELECT COUNT(*) FROM reports q WHERE q.project_id = sqlc.arg(project_id)
       AND q.delivery_state = 'pending' AND q.delivery_attempts = 0
       AND q.id IN (SELECT report_id FROM report_quiet)) >= CAST(sqlc.arg(min_count) AS INTEGER);

-- name: LatestOpenNeedsInputReports :many
-- Each listed session's newest needs_input report, unless the same session
-- filed a later done or stuck report (its request was answered or overtaken).
SELECT r.session_id, r.id, r.note, r.created_at
FROM reports r
WHERE r.state = 'needs_input'
  AND r.session_id IN (SELECT CAST(j.value AS TEXT) FROM json_each(sqlc.arg(session_ids)) AS j)
  AND NOT EXISTS (
    SELECT 1 FROM reports later
    WHERE later.session_id = r.session_id
      AND (later.created_at > r.created_at OR (later.created_at = r.created_at AND later.id > r.id))
      AND later.state IN ('needs_input', 'done', 'stuck')
  )
ORDER BY r.session_id;
