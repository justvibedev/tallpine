-- name: GetSessionResearch :one
-- An orchestrator's persisted research mode (B736); research_since NULL is off.
SELECT research_since, research_window, research_last_turn_at FROM sessions WHERE id = sqlc.arg(id);

-- name: SetSessionResearch :exec
-- Writes (or, with all NULL, clears) research mode; an unchanged row is left
-- alone so clients do not refetch for nothing.
UPDATE sessions
SET revision = revision + 1,
    research_since = sqlc.narg(research_since),
    research_window = sqlc.narg(research_window),
    research_last_turn_at = sqlc.narg(research_last_turn_at)
WHERE id = sqlc.arg(id)
  AND (research_since IS NOT sqlc.narg(research_since)
    OR research_window IS NOT sqlc.narg(research_window)
    OR research_last_turn_at IS NOT sqlc.narg(research_last_turn_at));

-- name: TouchSessionResearch :exec
-- Records a finished turn; changes nothing while research mode is off.
UPDATE sessions
SET revision = revision + 1, research_last_turn_at = sqlc.arg(at)
WHERE id = sqlc.arg(id) AND research_since IS NOT NULL;

-- name: ClearAllSessionResearch :exec
-- Ends every persisted research mode, for the global toggle turning off.
UPDATE sessions
SET revision = revision + 1, research_since = NULL, research_window = NULL, research_last_turn_at = NULL
WHERE research_since IS NOT NULL;
