-- Owner-message relay: a session's own chat is the conversation it owns (a
-- worker's session conversation) or the project conversation it currently
-- controls (an orchestrator). spawn_brief flags an opening task brief: the
-- conversation's first user turn, or text equal to a session's spawn prompt in
-- the project. Briefs are stored as human turns even when an agent or the
-- daemon wrote them, so a relay refuses them (fail closed).

-- name: SelectOwnChatMessage :one
SELECT m.id, m.role, m.origin, m.sender_session_id, m.text, m.created_at,
  CAST(EXISTS (SELECT 1 FROM sessions s WHERE s.project_id = c.project_id AND s.prompt <> '' AND s.prompt = m.text)
    OR m.sequence = (SELECT MIN(f.sequence) FROM conversation_messages f WHERE f.conversation_id = m.conversation_id AND f.role = 'user') AS INTEGER) AS spawn_brief
FROM conversation_messages m JOIN conversations c ON c.id = m.conversation_id
WHERE m.id = sqlc.arg(message_id)
  AND (c.session_id = sqlc.arg(session_id) OR (c.scope = 'project' AND c.current_session_id = sqlc.arg(session_id)));

-- name: SelectRecentOwnChatHumanMessages :many
SELECT m.id, m.role, m.origin, m.sender_session_id, m.text, m.created_at,
  CAST(EXISTS (SELECT 1 FROM sessions s WHERE s.project_id = c.project_id AND s.prompt <> '' AND s.prompt = m.text)
    OR m.sequence = (SELECT MIN(f.sequence) FROM conversation_messages f WHERE f.conversation_id = m.conversation_id AND f.role = 'user') AS INTEGER) AS spawn_brief
FROM conversation_messages m JOIN conversations c ON c.id = m.conversation_id
WHERE (c.session_id = sqlc.arg(session_id) OR (c.scope = 'project' AND c.current_session_id = sqlc.arg(session_id)))
  AND m.role = 'user' AND m.origin = 'human' AND m.sender_session_id = ''
ORDER BY m.created_at DESC, m.sequence DESC
LIMIT sqlc.arg(max_rows);
