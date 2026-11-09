-- Lifecycle facts for backlog items: what each item is linked to and how the
-- linked sessions and PRs stand. Everything is read from the source tables so
-- the reconciler stays idempotent.

-- name: SelectLiveBacklogItemsLinkedTo :many
SELECT i.* FROM backlog_items i
JOIN backlog_links l ON l.item_id = i.id
WHERE l.kind = ? AND l.ref = ? AND i.state IN ('inbox', 'open')
ORDER BY i.id;

-- name: SelectLiveBacklogItemsWithRuntimeLinks :many
SELECT DISTINCT i.* FROM backlog_items i
JOIN backlog_links l ON l.item_id = i.id
WHERE i.state IN ('inbox', 'open') AND l.kind IN ('session', 'pr')
ORDER BY i.id;

-- name: SelectBacklogLinkedSessions :many
-- A purged session counts as terminated.
SELECT l.ref AS session_id, CAST(COALESCE(s.is_terminated, 1) AS INTEGER) AS terminated, l.created_at AS linked_at
FROM backlog_links l
LEFT JOIN sessions s ON s.id = l.ref
WHERE l.item_id = ? AND l.kind = 'session'
ORDER BY l.created_at, l.ref;

-- name: SelectBacklogPRFacts :many
-- PRs reachable from an item: linked directly, or owned by a linked session.
-- direct marks a pr link; created_at_provider lets the caller drop PRs a
-- reused session opened before it was linked to the item.
SELECT p.url, p.number, p.pr_state, p.title, p.session_id, p.created_at_provider,
  CAST(p.url IN (SELECT bl.ref FROM backlog_links bl WHERE bl.item_id = sqlc.arg(target_item) AND bl.kind = 'pr') AS INTEGER) AS direct
FROM pr p
WHERE p.url IN (SELECT bl.ref FROM backlog_links bl WHERE bl.item_id = sqlc.arg(target_item) AND bl.kind = 'pr')
   OR p.session_id IN (SELECT bl.ref FROM backlog_links bl WHERE bl.item_id = sqlc.arg(target_item) AND bl.kind = 'session')
ORDER BY p.number, p.url;

-- name: SelectBacklogUntrackedPRLinks :many
-- pr links whose URL Tallpine does not track (no pr row): unverifiable, so never "merged".
SELECT l.ref FROM backlog_links l
WHERE l.item_id = ? AND l.kind = 'pr' AND l.ref NOT IN (SELECT url FROM pr);

-- name: SelectBacklogBadgeFacts :many
SELECT i.id AS item_id,
  CAST((SELECT count(*) FROM backlog_links l JOIN sessions s ON s.id = l.ref
        WHERE l.item_id = i.id AND l.kind = 'session' AND s.is_terminated = 0) AS INTEGER) AS active_sessions,
  CAST((SELECT count(*) FROM pr p WHERE p.pr_state IN ('draft', 'open')
        AND (p.url IN (SELECT ref FROM backlog_links WHERE item_id = i.id AND kind = 'pr')
          OR p.session_id IN (SELECT ref FROM backlog_links WHERE item_id = i.id AND kind = 'session'))) AS INTEGER) AS open_prs,
  CAST((SELECT count(*) FROM pr p WHERE p.pr_state = 'merged'
        AND (p.url IN (SELECT ref FROM backlog_links WHERE item_id = i.id AND kind = 'pr')
          OR p.session_id IN (SELECT ref FROM backlog_links WHERE item_id = i.id AND kind = 'session'))) AS INTEGER) AS merged_prs,
  CAST((SELECT count(*) FROM pr p WHERE p.pr_state = 'closed'
        AND (p.url IN (SELECT ref FROM backlog_links WHERE item_id = i.id AND kind = 'pr')
          OR p.session_id IN (SELECT ref FROM backlog_links WHERE item_id = i.id AND kind = 'session'))) AS INTEGER) AS closed_prs,
  CAST((SELECT count(*) FROM backlog_links l WHERE l.item_id = i.id AND l.kind = 'pr'
        AND l.ref NOT IN (SELECT url FROM pr)) AS INTEGER) AS untracked_prs
FROM backlog_items i
WHERE i.project_id = sqlc.arg(project_id) AND (sqlc.arg(item_id) = '' OR i.id = sqlc.arg(item_id));

-- name: SelectExpiredBacklogInbox :many
-- Untriaged agent-filed inbox items past the cutoff. Human-origin items never expire.
SELECT * FROM backlog_items
WHERE state = 'inbox' AND origin_kind <> 'human' AND updated_at < ?
ORDER BY updated_at, id;

-- name: SelectTerminatedSessionIDsWithOpenBacklogPRs :many
-- Ended sessions whose PR is still open or draft: the SCM observer keeps polling
-- those PRs so a merge that lands after the worker was killed is still recorded,
-- whether or not a live backlog item is linked (B397).
SELECT DISTINCT s.id FROM sessions s
WHERE s.is_terminated = 1
  AND EXISTS (SELECT 1 FROM pr p WHERE p.session_id = s.id AND p.pr_state IN ('open', 'draft'))
ORDER BY s.id;

-- name: AckBacklogMergedPRsOnReopen :exec
-- Records every merged PR reachable from the item (direct link or linked session).
INSERT OR IGNORE INTO backlog_reopen_acks (item_id, pr_url)
SELECT sqlc.arg(target_item), p.url FROM pr p
WHERE p.pr_state = 'merged'
  AND (p.url IN (SELECT bl.ref FROM backlog_links bl WHERE bl.item_id = sqlc.arg(target_item) AND bl.kind = 'pr')
    OR p.session_id IN (SELECT bl.ref FROM backlog_links bl WHERE bl.item_id = sqlc.arg(target_item) AND bl.kind = 'session'));

-- name: SelectBacklogReopenAcks :many
SELECT pr_url FROM backlog_reopen_acks WHERE item_id = ?;
