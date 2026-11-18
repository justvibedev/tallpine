-- Conversation history retention below the last settled compaction boundary.
-- Run by the archive janitor in small batches, never from the projection path.
--
-- NOTE: keep these comments ASCII. sqlc locates its star-expansion edits by byte
-- offset, so a multi-byte character here silently corrupts later queries.

-- Conversations that compacted at least once, with their retention progress.
-- Reads only idx_conversation_provider_events_compacted. A conversation with
-- nothing left to do is filtered out: its latest compaction is the boundary
-- already worked through, and both cursors finished that boundary.
-- name: ListHistoryRetentionCandidates :many
SELECT e.conversation_id,
       CAST(MAX(e.id) AS INTEGER) AS latest_compaction_id,
       CAST(COALESCE(r.boundary_event_id, 0) AS INTEGER) AS boundary_event_id,
       CAST(COALESCE(r.events_pruned_through_id, 0) AS INTEGER) AS events_pruned_through_id,
       CAST(COALESCE(r.activities_pruned_through_seq, 0) AS INTEGER) AS activities_pruned_through_seq,
       CAST(COALESCE(r.activities_done_boundary_id, 0) AS INTEGER) AS activities_done_boundary_id
FROM conversation_provider_events e INDEXED BY idx_conversation_provider_events_compacted
LEFT JOIN conversation_history_retention r ON r.conversation_id = e.conversation_id
WHERE e.method = 'thread.compacted'
GROUP BY e.conversation_id
HAVING MAX(e.id) > COALESCE(r.boundary_event_id, 0)
    OR COALESCE(r.events_pruned_through_id, 0) < COALESCE(r.boundary_event_id, 0)
    OR COALESCE(r.activities_done_boundary_id, 0) < COALESCE(r.boundary_event_id, 0)
ORDER BY e.conversation_id;

-- The newest compaction above after_id that has settled: a later turn.started
-- exists in the conversation. A persistent host refuses a new prompt until the
-- previous one is acknowledged, so a later turn proves every event before the
-- compaction belongs to an acknowledged prompt the host can no longer replay.
-- name: SettledHistoryCompactionBoundary :one
SELECT CAST(COALESCE(MAX(c.id), 0) AS INTEGER) AS id
FROM conversation_provider_events c INDEXED BY idx_conversation_provider_events_compacted
WHERE c.conversation_id = sqlc.arg(conversation_id)
  AND c.method = 'thread.compacted'
  AND c.id > sqlc.arg(after_id)
  AND EXISTS (
      SELECT 1 FROM conversation_provider_events s
      WHERE s.conversation_id = c.conversation_id
        AND s.id > c.id
        AND s.method = 'turn.started'
  );

-- name: HistoryBoundaryReceivedAt :one
SELECT received_at FROM conversation_provider_events WHERE id = sqlc.arg(id);

-- One window of a conversation's archive strictly below the boundary. Reads no
-- payload.
-- name: SelectProviderEventsForHistoryPrune :many
SELECT e.id, e.method, e.provider_event_id
FROM conversation_provider_events e
WHERE e.conversation_id = sqlc.arg(conversation_id)
  AND e.id > sqlc.arg(after_id)
  AND e.id < sqlc.arg(boundary_id)
ORDER BY e.id
LIMIT sqlc.arg(page_limit);

-- One window of a conversation's activities above the cursor, in sequence
-- order, with the state of the turn each belongs to ('' without one). The
-- caller stops at the first activity created at or after the boundary.
-- name: SelectActivitiesForHistoryPrune :many
SELECT a.id, a.sequence, a.kind, a.status, a.created_at,
       a.detail_json, a.command_output, a.command_output_truncated,
       a.streamed_text, a.streamed_text_truncated,
       CAST(COALESCE(t.state, '') AS TEXT) AS turn_state
FROM conversation_activities a
LEFT JOIN conversation_turns t ON t.id = a.turn_id
WHERE a.conversation_id = sqlc.arg(conversation_id)
  AND a.sequence > sqlc.arg(after_sequence)
ORDER BY a.sequence
LIMIT sqlc.arg(page_limit);

-- Rewrites a settled activity's bulky payload in place. revision is left alone
-- on purpose: the activity's meaning did not change, so no change_log row is
-- written and open clients are not asked to refetch years of history.
-- name: PruneActivityPayload :exec
UPDATE conversation_activities
SET detail_json = sqlc.arg(detail_json),
    command_output = sqlc.arg(command_output),
    command_output_truncated = sqlc.arg(command_output_truncated),
    streamed_text = sqlc.arg(streamed_text),
    streamed_text_truncated = sqlc.arg(streamed_text_truncated)
WHERE id = sqlc.arg(id);

-- name: UpsertHistoryRetention :exec
INSERT INTO conversation_history_retention (
    conversation_id, boundary_event_id, events_pruned_through_id,
    activities_pruned_through_seq, activities_done_boundary_id, updated_at
) VALUES (
    sqlc.arg(conversation_id), sqlc.arg(boundary_event_id), sqlc.arg(events_pruned_through_id),
    sqlc.arg(activities_pruned_through_seq), sqlc.arg(activities_done_boundary_id), sqlc.arg(updated_at)
)
ON CONFLICT (conversation_id) DO UPDATE SET
    boundary_event_id = MAX(boundary_event_id, excluded.boundary_event_id),
    events_pruned_through_id = MAX(events_pruned_through_id, excluded.events_pruned_through_id),
    activities_pruned_through_seq = MAX(activities_pruned_through_seq, excluded.activities_pruned_through_seq),
    activities_done_boundary_id = MAX(activities_done_boundary_id, excluded.activities_done_boundary_id),
    updated_at = excluded.updated_at;
