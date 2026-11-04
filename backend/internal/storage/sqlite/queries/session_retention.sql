-- Session retention (B161). Every delete is one bounded batch so a purge
-- never holds the writer for long. Kept on purpose: sessions, conversations,
-- conversation_turns, usage, model usage, 'system' activities (compaction
-- markers), reviews and PRs.

-- name: SessionProviderEventFootprint :one
SELECT CAST(COUNT(*) AS INTEGER) AS rows_count,
       CAST(COALESCE(SUM(length(payload_json) + length(provider_event_id) + length(method)), 0) AS INTEGER) AS bytes
FROM conversation_provider_events
WHERE session_id = sqlc.arg(session_id);

-- name: SessionTranscriptFootprint :one
SELECT CAST(
         (SELECT COUNT(*) FROM conversation_messages m
            JOIN conversations c ON c.id = m.conversation_id
           WHERE c.session_id = sqlc.arg(session_id) AND c.scope = 'session')
       + (SELECT COUNT(*) FROM conversation_activities a
            JOIN conversations c ON c.id = a.conversation_id
           WHERE c.session_id = sqlc.arg(session_id) AND c.scope = 'session' AND a.kind <> 'system')
       + (SELECT COUNT(*) FROM session_terminal_turns t WHERE t.session_id = sqlc.arg(session_id))
       AS INTEGER) AS rows_count,
       CAST(
         (SELECT COALESCE(SUM(length(m.text)), 0) FROM conversation_messages m
            JOIN conversations c ON c.id = m.conversation_id
           WHERE c.session_id = sqlc.arg(session_id) AND c.scope = 'session')
       + (SELECT COALESCE(SUM(length(a.summary) + COALESCE(length(a.detail_json), 0)), 0) FROM conversation_activities a
            JOIN conversations c ON c.id = a.conversation_id
           WHERE c.session_id = sqlc.arg(session_id) AND c.scope = 'session' AND a.kind <> 'system')
       + (SELECT COALESCE(SUM(COALESCE(length(t.user_request), 0) + COALESCE(length(t.agent_response), 0)), 0)
            FROM session_terminal_turns t WHERE t.session_id = sqlc.arg(session_id))
       AS INTEGER) AS bytes;

-- name: DeleteSessionProviderEventsBatch :execrows
DELETE FROM conversation_provider_events
WHERE id IN (
    SELECT e.id FROM conversation_provider_events e
    WHERE e.session_id = sqlc.arg(session_id)
    LIMIT sqlc.arg(page_limit)
);

-- name: DeleteSessionProviderEventRetentionCursor :exec
DELETE FROM session_provider_event_retention WHERE session_id = sqlc.arg(session_id);

-- name: DeleteSessionMessagesBatch :execrows
DELETE FROM conversation_messages
WHERE id IN (
    SELECT m.id FROM conversation_messages m
    JOIN conversations c ON c.id = m.conversation_id
    WHERE c.session_id = sqlc.arg(session_id) AND c.scope = 'session'
    LIMIT sqlc.arg(page_limit)
);

-- name: DeleteSessionActivitiesBatch :execrows
DELETE FROM conversation_activities
WHERE id IN (
    SELECT a.id FROM conversation_activities a
    JOIN conversations c ON c.id = a.conversation_id
    WHERE c.session_id = sqlc.arg(session_id) AND c.scope = 'session' AND a.kind <> 'system'
    LIMIT sqlc.arg(page_limit)
);

-- name: DeleteSessionTerminalTurnsBatch :execrows
DELETE FROM session_terminal_turns
WHERE id IN (
    SELECT t.id FROM session_terminal_turns t
    WHERE t.session_id = sqlc.arg(session_id)
    LIMIT sqlc.arg(page_limit)
);

-- name: DeleteSessionLastAssistantMessage :exec
DELETE FROM session_last_assistant_messages WHERE session_id = sqlc.arg(session_id);

-- name: UpsertSessionRetentionPurge :exec
INSERT INTO session_retention_purges (session_id, purged_at, provider_events, transcripts)
VALUES (sqlc.arg(session_id), sqlc.arg(purged_at), sqlc.arg(provider_events), sqlc.arg(transcripts))
ON CONFLICT (session_id) DO UPDATE SET
    purged_at = excluded.purged_at,
    provider_events = MAX(provider_events, excluded.provider_events),
    transcripts = MAX(transcripts, excluded.transcripts);

-- name: GetSessionRetentionPurge :one
SELECT session_id, purged_at, provider_events, transcripts
FROM session_retention_purges
WHERE session_id = sqlc.arg(session_id);
