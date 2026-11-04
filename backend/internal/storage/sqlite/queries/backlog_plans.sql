-- name: InsertBacklogPlan :exec
INSERT INTO backlog_plans (
    id, project_id, item_id, status, planner_session_id, base_commit, base_branch,
    item_updated_at_start, started_at, updated_at, notify_session_id
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: SelectBacklogPlan :one
SELECT * FROM backlog_plans WHERE id = ?;

-- name: SelectOpenBacklogPlanForItem :one
SELECT * FROM backlog_plans
WHERE item_id = ? AND status NOT IN ('approved', 'superseded', 'failed');

-- name: SelectCurrentBacklogPlanForItem :one
SELECT * FROM backlog_plans
WHERE item_id = ? AND status <> 'superseded'
-- An open plan is current; else an approved one stays in effect over a failed
-- re-plan; else the newest.
ORDER BY (status NOT IN ('approved', 'failed')) DESC, (status = 'approved') DESC, started_at DESC, id DESC LIMIT 1;

-- name: SelectBacklogPlanByPlanner :one
SELECT * FROM backlog_plans WHERE planner_session_id = ? ORDER BY started_at DESC LIMIT 1;

-- name: UpdateBacklogPlan :exec
UPDATE backlog_plans SET
    status = sqlc.arg(status),
    planner_session_id = sqlc.arg(planner_session_id),
    base_commit = sqlc.arg(base_commit),
    base_branch = sqlc.arg(base_branch),
    questions_json = sqlc.arg(questions_json),
    assumptions_json = sqlc.arg(assumptions_json),
    answers_json = sqlc.arg(answers_json),
    plan_markdown = sqlc.arg(plan_markdown),
    decisions_markdown = sqlc.arg(decisions_markdown),
    pr_split_json = sqlc.arg(pr_split_json),
    review_focus_json = sqlc.arg(review_focus_json),
    approved_by = sqlc.arg(approved_by),
    approved_at = sqlc.arg(approved_at),
    owner_message_id = sqlc.arg(owner_message_id),
    build_anyway = sqlc.arg(build_anyway),
    item_changed_at = sqlc.arg(item_changed_at),
    updated_at = sqlc.arg(updated_at),
    failure = sqlc.arg(failure)
WHERE id = sqlc.arg(id);

-- name: SelectConversationMessageSource :one
SELECT m.role, m.origin, m.sender_session_id, m.created_at, c.project_id
FROM conversation_messages m JOIN conversations c ON c.id = m.conversation_id
WHERE m.id = ?;

-- name: SelectLatestHumanProjectMessage :one
SELECT m.id, m.created_at
FROM conversation_messages m JOIN conversations c ON c.id = m.conversation_id
WHERE c.project_id = ? AND c.scope = 'project' AND m.role = 'user' AND m.origin = 'human' AND m.sender_session_id = ''
ORDER BY m.created_at DESC, m.sequence DESC LIMIT 1;

-- name: SelectOpenBacklogPlansForProject :many
SELECT * FROM backlog_plans
WHERE project_id = ? AND status NOT IN ('approved', 'superseded')
ORDER BY started_at, id;
