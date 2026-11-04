-- name: AllocateBacklogSeq :one
-- Returns the ref number just allocated for the project (starts at 1).
INSERT INTO backlog_counters (project_id, next_seq) VALUES (?, 2)
ON CONFLICT (project_id) DO UPDATE SET next_seq = next_seq + 1
RETURNING next_seq - 1;
