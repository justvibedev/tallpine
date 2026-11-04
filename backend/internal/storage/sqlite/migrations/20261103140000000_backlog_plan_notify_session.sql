-- Plan notices go to the orchestrator that started the plan (B485), not to
-- whichever live orchestrator the project lists first.

-- +goose Up
ALTER TABLE backlog_plans ADD COLUMN notify_session_id TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE backlog_plans DROP COLUMN notify_session_id;
