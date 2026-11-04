-- name: InsertTurnFallback :exec
INSERT INTO turn_fallbacks (id, session_id, mode, generation, native_id, turn_id)
VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT (id) DO NOTHING;

-- name: CompleteTurnFallback :exec
UPDATE turn_fallbacks SET completed_at = ?, due_at = ?, excerpt = ?,
 disposition = CASE WHEN disposition = 'active' THEN 'pending' ELSE disposition END
WHERE id = ? AND completed_at IS NULL;

-- Retire all previous owners at an accepted work boundary; shared by Chat
-- intake/primary acceptance and terminal activity transactions.
-- name: SupersedeTerminalFallbacks :exec
UPDATE turn_fallbacks SET disposition = 'superseded'
WHERE session_id = ? AND id != ? AND disposition != 'superseded';

-- Intake is not a start: retire completed results, but keep an undelivered
-- failure notice so a held queue (failure holds it) cannot drop it. The next
-- turn's start/bind supersedes whatever remains.
-- name: SupersedeCompletedFallbacks :exec
UPDATE turn_fallbacks SET disposition = 'superseded'
WHERE session_id = ? AND completed_at IS NOT NULL AND disposition != 'superseded'
 AND NOT (disposition = 'pending' AND EXISTS (
  SELECT 1 FROM conversation_turns t WHERE t.id = turn_fallbacks.turn_id AND t.state = 'failed'));

-- name: SuppressTurnFallback :exec
UPDATE turn_fallbacks SET disposition = 'suppressed'
WHERE id = ? AND disposition IN ('active', 'pending');

-- name: SuppressTerminalFallback :exec
UPDATE turn_fallbacks SET disposition = 'suppressed'
WHERE session_id = ? AND mode = 'tui' AND generation = ?
 AND disposition IN ('active', 'pending');

-- Current ownership is advanced by committed intake/start transactions, never
-- inferred from turn insertion order (queued turns may execute in any order).
-- name: CurrentReportableChatTurn :one
SELECT t.* FROM conversation_turns t
JOIN turn_fallbacks f ON f.turn_id = t.id AND f.mode = 'chat'
WHERE f.session_id = ? AND f.disposition != 'superseded'
 AND t.handled_by_review_id IS NULL AND t.state IN ('running', 'completed', 'failed')
LIMIT 1;

-- name: GetTurnFallback :one
SELECT * FROM turn_fallbacks WHERE id = ?;

-- name: ListPendingTurnFallbacks :many
SELECT * FROM turn_fallbacks WHERE disposition = 'pending' ORDER BY due_at, id;

-- name: HasCurrentCompletedTurnFallback :one
SELECT EXISTS (SELECT 1 FROM turn_fallbacks
 WHERE session_id = ? AND completed_at IS NOT NULL
 AND disposition IN ('pending', 'suppressed', 'emitted'));

-- name: SupersedeInvalidTurnFallbacks :exec
UPDATE turn_fallbacks SET disposition = 'superseded'
WHERE disposition != 'superseded' AND carry_transition_id = '' AND (
 NOT EXISTS (SELECT 1 FROM sessions s WHERE s.id = turn_fallbacks.session_id
  AND s.is_terminated = 0 AND s.kind = 'worker' AND s.session_mode = turn_fallbacks.mode)
 OR (mode = 'tui' AND NOT EXISTS (SELECT 1 FROM sessions s WHERE s.id = turn_fallbacks.session_id
  AND s.runtime_launch_id = turn_fallbacks.generation AND s.agent_session_id = turn_fallbacks.native_id))
 OR (mode = 'chat' AND NOT EXISTS (SELECT 1 FROM sessions s WHERE s.id = turn_fallbacks.session_id AND s.controller_generation = turn_fallbacks.generation))
 OR (mode = 'chat' AND NOT EXISTS (
  SELECT 1 FROM conversation_turns t JOIN conversations c ON c.id = t.conversation_id
  WHERE t.id = turn_fallbacks.turn_id AND c.current_session_id = turn_fallbacks.session_id
 ))
);

-- name: SupersedeInvalidCarriedTurnFallbacks :exec
UPDATE turn_fallbacks SET disposition = 'superseded'
WHERE disposition != 'superseded' AND carry_transition_id != '' AND NOT EXISTS (
 SELECT 1 FROM sessions s WHERE s.id = turn_fallbacks.session_id
 AND s.kind = 'worker' AND s.is_terminated = 0 AND s.session_mode = owner_mode
 AND (CASE WHEN owner_mode = 'chat' THEN s.provider_conversation_id ELSE s.agent_session_id END) = owner_native_id
 AND ((CASE WHEN owner_mode = 'chat' THEN s.controller_generation ELSE s.runtime_launch_id END) = owner_generation
  OR (owner_claimed = 0
   AND (CASE WHEN owner_mode = 'chat' THEN s.controller_generation ELSE s.runtime_launch_id END) = ''
   AND carry_transition_id = (SELECT t.id FROM session_interface_transitions t WHERE t.session_id = s.id ORDER BY t.created_at DESC, t.rowid DESC LIMIT 1)))
);

-- name: CarryTurnFallbacksThroughEpoch :exec
UPDATE turn_fallbacks SET carry_transition_id = ?, owner_mode = ?, owner_generation = ?, owner_native_id = ?, owner_claimed = 0
WHERE session_id = ? AND completed_at IS NOT NULL AND disposition IN ('pending', 'emitted', 'suppressed');

-- name: ClaimCarriedTurnFallbacks :exec
UPDATE turn_fallbacks SET owner_claimed = 1
WHERE session_id = ? AND carry_transition_id != '' AND disposition != 'superseded'
 AND EXISTS (SELECT 1 FROM sessions s WHERE s.id = turn_fallbacks.session_id AND s.session_mode = owner_mode
 AND (CASE WHEN owner_mode = 'chat' THEN s.controller_generation ELSE s.runtime_launch_id END) = owner_generation
 AND (CASE WHEN owner_mode = 'chat' THEN s.provider_conversation_id ELSE s.agent_session_id END) = owner_native_id);

-- name: GetTurnFallbackHandoffClaim :one
SELECT owner_generation, owner_mode, owner_native_id, owner_claimed, carry_transition_id FROM turn_fallbacks
WHERE session_id = ? AND carry_transition_id != '' AND disposition IN ('pending', 'emitted', 'suppressed') LIMIT 1;

-- name: CarryLiveChatFallbackOwner :exec
UPDATE turn_fallbacks SET owner_generation = sqlc.arg(new_generation), owner_claimed = 1
WHERE session_id = sqlc.arg(session_id) AND carry_transition_id != '' AND owner_mode = 'chat'
 AND owner_generation = sqlc.arg(old_generation) AND disposition != 'superseded';

-- name: SuppressCarriedTurnFallbacks :exec
UPDATE turn_fallbacks SET disposition = 'suppressed'
WHERE session_id = ? AND carry_transition_id != '' AND disposition = 'pending';

-- name: EmitTurnFallback :execrows
UPDATE turn_fallbacks SET disposition = 'emitted'
WHERE id = ? AND disposition = 'pending' AND due_at <= ?;

-- name: LatestTurnFallbackAssistant :one
SELECT text FROM conversation_messages
WHERE turn_id = ? AND role = 'assistant' ORDER BY sequence DESC LIMIT 1;

-- name: CarryChatTurnFallbacks :exec
UPDATE turn_fallbacks SET generation = sqlc.arg(new_generation)
WHERE session_id = sqlc.arg(session_id) AND carry_transition_id = '' AND mode = 'chat' AND generation = sqlc.arg(old_generation)
 AND disposition IN ('active', 'pending', 'suppressed', 'emitted');
