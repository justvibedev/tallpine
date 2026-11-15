-- Summary: timed orchestrator follow-ups: an intent may carry a wake time, and records when its one wake was delivered.
-- +goose Up
-- +goose StatementBegin
ALTER TABLE orchestrator_intents ADD COLUMN wake_at TIMESTAMP;
ALTER TABLE orchestrator_intents ADD COLUMN woken_at TIMESTAMP;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE orchestrator_intents DROP COLUMN woken_at;
ALTER TABLE orchestrator_intents DROP COLUMN wake_at;
-- +goose StatementEnd
