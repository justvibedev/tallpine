-- Provider-event identity keys and the one-time tombstone sweep (B832).
--
-- NOTE: keep these comments ASCII. sqlc locates its star-expansion edits by byte
-- offset, so a multi-byte character here silently corrupts later queries.

-- The newest turn.started of a conversation, read backwards through
-- idx_conversation_provider_events_replay. A persistent host refuses a new
-- prompt until the previous one is acknowledged, so a later turn.started proves
-- every acp-host event before it can never be replayed.
-- name: LatestProviderTurnStartedID :one
SELECT CAST(COALESCE((
    SELECT e.id FROM conversation_provider_events e
    WHERE e.conversation_id = sqlc.arg(conversation_id) AND e.method = 'turn.started'
    ORDER BY e.id DESC LIMIT 1
), 0) AS INTEGER) AS id;

-- name: GetProviderEventTombstoneSweep :one
SELECT swept_through_id, through_id FROM provider_event_tombstone_sweep WHERE id = 1;

-- One window of the sweep: up to page_limit rows above the cursor, each with
-- whether it is a '{}' tombstone and whether the session that wrote it is
-- terminated. octet_length is checked first, so a large payload is never read
-- off its overflow pages.
-- name: SelectProviderEventsForTombstoneSweep :many
SELECT e.id, e.conversation_id, e.provider_event_id,
       CAST(octet_length(e.payload_json) = 2 AND e.payload_json = '{}' AS INTEGER) AS tombstone,
       CAST(COALESCE((SELECT s.is_terminated FROM sessions s WHERE s.id = e.session_id), 0) AS INTEGER) AS session_terminated
FROM conversation_provider_events e
WHERE e.id > sqlc.arg(after_id) AND e.id <= sqlc.arg(through_id)
ORDER BY e.id
LIMIT sqlc.arg(page_limit);

-- name: AdvanceProviderEventTombstoneSweep :exec
UPDATE provider_event_tombstone_sweep
SET swept_through_id = MAX(swept_through_id, sqlc.arg(swept_through_id)), updated_at = sqlc.arg(updated_at)
WHERE id = 1;

-- What compaction's decision to hold a delta depends on, read without the
-- writer: the conversation's newest event, its turns and how many are still
-- open, and the termination of its sessions. While none of these change, a
-- held delta stays held, so the janitor need not scan it again.
-- name: GetProviderEventHoldMark :one
SELECT CAST(COALESCE((
           SELECT e.id FROM conversation_provider_events e
           WHERE e.conversation_id = c.id ORDER BY e.id DESC LIMIT 1
       ), 0) AS INTEGER) AS latest_event_id,
       CAST((SELECT COUNT(*) FROM conversation_turns t WHERE t.conversation_id = c.id) AS INTEGER) AS turns,
       CAST((
           SELECT COUNT(*) FROM conversation_turns t
           WHERE t.conversation_id = c.id AND t.state IN ('queued', 'running')
       ) AS INTEGER) AS open_turns,
       CAST(COALESCE(c.current_session_id, '') AS TEXT) AS current_session_id,
       CAST((
           SELECT COUNT(*) FROM sessions s
           WHERE s.is_terminated AND (s.id = c.session_id OR s.id = c.current_session_id)
       ) AS INTEGER) AS terminated_sessions
FROM conversations c
WHERE c.id = sqlc.arg(conversation_id);
