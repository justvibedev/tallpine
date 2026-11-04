-- name: DeletePRPaths :exec
DELETE FROM pr_paths WHERE pr_url = ?;

-- name: InsertPRPath :exec
INSERT OR IGNORE INTO pr_paths (pr_url, path) VALUES (?, ?);

-- name: ListMergeDigestPRs :many
-- A project's open or draft PRs plus those merged since sqlc.arg(merged_since),
-- with the lane of the session that owns each. A terminated session's PR is no
-- longer polled, so its stored 'open' state may be stale: it counts as in flight
-- only if it changed since merged_since.
SELECT p.url, p.number, p.pr_state, p.title, p.head_sha, p.session_id,
       p.updated_at, p.merged_at_provider,
       COALESCE(l.lane_item_id, '') AS lane_item_id
FROM pr p
JOIN sessions s ON s.id = p.session_id
LEFT JOIN session_lane l ON l.session_id = p.session_id
WHERE s.project_id = sqlc.arg(project_id)
  AND ((p.pr_state IN ('open', 'draft') AND (NOT s.is_terminated OR p.updated_at >= sqlc.arg(merged_since)))
       OR (p.pr_state = 'merged' AND COALESCE(p.merged_at_provider, p.state_changed_at, p.updated_at) >= sqlc.arg(merged_since)))
ORDER BY p.updated_at DESC;

-- name: ListPRPathsByURLs :many
-- Changed paths of the PRs ListMergeDigestPRs returned, so the digest filter
-- lives in that one query.
SELECT pr_url, path FROM pr_paths WHERE pr_url IN (sqlc.slice(urls));
