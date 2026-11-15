-- +goose Up
-- An orchestrator's research mode (B736), persisted so a daemon restart keeps
-- it. research_since NULL means research mode is off; research_window is the
-- window chosen with `research on --window` ('' follows the project setting);
-- research_last_turn_at is the last turn the quiet-hour expiry runs from.
ALTER TABLE sessions ADD COLUMN research_since TIMESTAMP;
ALTER TABLE sessions ADD COLUMN research_window TEXT;
ALTER TABLE sessions ADD COLUMN research_last_turn_at TIMESTAMP;

-- +goose Down
ALTER TABLE sessions DROP COLUMN research_last_turn_at;
ALTER TABLE sessions DROP COLUMN research_window;
ALTER TABLE sessions DROP COLUMN research_since;
