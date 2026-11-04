-- +goose Up
ALTER TABLE conversation_turns ADD COLUMN batched_input INTEGER NOT NULL DEFAULT 0;
ALTER TABLE conversation_turns ADD COLUMN provider_input_text TEXT NOT NULL DEFAULT '';
-- How a queued message reached the provider: 'steered' into a running turn or
-- 'batched' into a drained turn. Queued-to-delivered delay is completed_at -
-- requested_at on the same row (B85 measurement).
ALTER TABLE conversation_turns ADD COLUMN delivery_mode TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE conversation_turns DROP COLUMN delivery_mode;
ALTER TABLE conversation_turns DROP COLUMN provider_input_text;
ALTER TABLE conversation_turns DROP COLUMN batched_input;
