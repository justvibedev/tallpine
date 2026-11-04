-- +goose Up
-- Kiro reports credits per turn, not tokens (B256): keep the running sum beside
-- the token counters.
ALTER TABLE conversation_model_usage ADD COLUMN credits REAL NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE conversation_model_usage DROP COLUMN credits;
