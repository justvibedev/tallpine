-- Provider-event archive compaction and retention. Run by the archive janitor
-- in small batches, never from the projection path.
--
-- NOTE: keep these comments ASCII. sqlc locates its star-expansion edits by byte
-- offset, so a multi-byte character here silently corrupts later queries.

-- Conversations with archived events above their compaction watermark.
-- name: ListProviderEventCompactionCandidates :many
SELECT c.id AS conversation_id,
       CAST(COALESCE(w.compacted_through_id, 0) AS INTEGER) AS compacted_through_id
FROM conversations c
LEFT JOIN conversation_provider_event_compaction w ON w.conversation_id = c.id
WHERE EXISTS (
    SELECT 1 FROM conversation_provider_events e
    WHERE e.conversation_id = c.id
      AND e.id > COALESCE(w.compacted_through_id, 0)
)
ORDER BY c.id;

-- One window of a conversation's archive above the watermark. Streamed deltas
-- carry the provider turn they belong to and that turn's projected state; any
-- other row reports empty strings, which the janitor keeps. A delta already
-- reduced to '{}' reports compacted = 1. Only delta payloads are read, so a
-- window never pulls a large activity payload off its overflow pages.
-- name: SelectProviderEventsForCompaction :many
SELECT e.id,
       e.method,
       e.provider_event_id,
       CAST(CASE
           WHEN e.method IN ('message.delta', 'reasoning.delta', 'command.output.delta', 'command.input', 'activity.text')
           THEN e.payload_json = '{}'
           ELSE 0
       END AS INTEGER) AS compacted,
       CAST(CASE
           WHEN e.method IN ('message.delta', 'reasoning.delta', 'command.output.delta', 'command.input', 'activity.text')
           THEN COALESCE(json_extract(e.payload_json, '$.providerTurnId'), '')
           ELSE ''
       END AS TEXT) AS provider_turn_id,
       CAST(CASE
           WHEN e.method IN ('message.delta', 'reasoning.delta', 'command.output.delta', 'command.input', 'activity.text')
           THEN COALESCE((
               SELECT t.state FROM conversation_turns t
               WHERE t.conversation_id = e.conversation_id
                 AND t.provider_turn_id = json_extract(e.payload_json, '$.providerTurnId')
                 AND t.provider_turn_id <> ''
               ORDER BY (t.state IN ('queued', 'running')) DESC
               LIMIT 1
           ), '')
           ELSE ''
       END AS TEXT) AS turn_state
FROM conversation_provider_events e
WHERE e.conversation_id = sqlc.arg(conversation_id)
  AND e.id > sqlc.arg(after_id)
ORDER BY e.id
LIMIT sqlc.arg(page_limit);

-- The newest archived turn.completed of a conversation, read per branch through
-- idx_conversation_provider_events_turn_completed. A streamed delta of a turn Tallpine
-- has no row for counts as settled once any later turn completed.
-- name: LatestProviderTurnCompletedID :one
SELECT CAST(COALESCE(MAX(e.id), 0) AS INTEGER) AS id
FROM conversation_branches b
JOIN conversation_provider_events e
  ON e.branch_id = b.id AND e.method = 'turn.completed' AND e.conversation_id = b.conversation_id
WHERE b.conversation_id = sqlc.arg(conversation_id);

-- A delta without a native identity is never deduplicated, so nothing depends
-- on its row once its turn settled.
-- name: DeleteProviderEventsByID :execrows
DELETE FROM conversation_provider_events WHERE id IN (sqlc.slice(ids));

-- A delta with a native identity keeps its row as a tombstone: the dedupe
-- index must still reject a replay of that identity, or a replayed delta would
-- append its text to the settled message a second time.
-- name: TombstoneProviderEventsByID :execrows
UPDATE conversation_provider_events SET payload_json = '{}'
WHERE id IN (sqlc.slice(ids)) AND payload_json <> '{}';

-- name: UpsertProviderEventCompactionWatermark :exec
INSERT INTO conversation_provider_event_compaction (conversation_id, compacted_through_id, updated_at)
VALUES (sqlc.arg(conversation_id), sqlc.arg(compacted_through_id), sqlc.arg(updated_at))
ON CONFLICT (conversation_id) DO UPDATE SET
    compacted_through_id = MAX(compacted_through_id, excluded.compacted_through_id),
    updated_at = excluded.updated_at;

-- Sessions terminated before the cutoff with archived events above their
-- expiry cursor. updated_at is the termination time unless something touched
-- the row later, which only delays expiry.
-- name: ListExpiredTerminatedSessionsWithProviderEvents :many
SELECT s.id AS session_id,
       CAST(COALESCE(r.expired_through_id, 0) AS INTEGER) AS expired_through_id
FROM sessions s
LEFT JOIN session_provider_event_retention r ON r.session_id = s.id
WHERE s.is_terminated = 1
  AND s.updated_at < sqlc.arg(before)
  AND EXISTS (
      SELECT 1 FROM conversation_provider_events e
      WHERE e.session_id = s.id AND e.id > COALESCE(r.expired_through_id, 0)
  )
ORDER BY s.updated_at
LIMIT sqlc.arg(page_limit);

-- One window of a session's archive above its expiry cursor. Reads no payload.
-- name: SelectSessionProviderEventsForRetention :many
SELECT e.id, e.provider_event_id
FROM conversation_provider_events e
WHERE e.session_id = sqlc.arg(session_id)
  AND e.id > sqlc.arg(after_id)
ORDER BY e.id
LIMIT sqlc.arg(page_limit);

-- name: UpsertSessionProviderEventRetention :exec
INSERT INTO session_provider_event_retention (session_id, expired_through_id, updated_at)
VALUES (sqlc.arg(session_id), sqlc.arg(expired_through_id), sqlc.arg(updated_at))
ON CONFLICT (session_id) DO UPDATE SET
    expired_through_id = MAX(expired_through_id, excluded.expired_through_id),
    updated_at = excluded.updated_at;
