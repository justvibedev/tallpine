-- +goose Up
-- On by default: an orchestrator may raise its compaction window while it
-- researches a large body of code (B413). Off clears every research mode.
ALTER TABLE app_settings ADD COLUMN dynamic_orchestrator_context INTEGER NOT NULL DEFAULT 1
    CHECK (dynamic_orchestrator_context IN (0, 1));

-- +goose Down
ALTER TABLE app_settings DROP COLUMN dynamic_orchestrator_context;
