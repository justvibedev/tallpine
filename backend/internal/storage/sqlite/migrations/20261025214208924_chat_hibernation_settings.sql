-- +goose Up
-- Idle Chat hibernation becomes a durable, user-visible preference, on by
-- default. A session sleeps only after its agent has been quiet this long:
-- workers after 20 minutes, orchestrators (which coordinate many workers and
-- lose more by a cold prompt cache) after an hour.
ALTER TABLE app_settings ADD COLUMN chat_hibernation INTEGER NOT NULL DEFAULT 1
    CHECK (chat_hibernation IN (0, 1));
ALTER TABLE app_settings ADD COLUMN chat_hibernation_worker_idle_seconds INTEGER NOT NULL DEFAULT 1200
    CHECK (chat_hibernation_worker_idle_seconds >= 60);
ALTER TABLE app_settings ADD COLUMN chat_hibernation_orchestrator_idle_seconds INTEGER NOT NULL DEFAULT 3600
    CHECK (chat_hibernation_orchestrator_idle_seconds >= 60);

-- +goose Down
ALTER TABLE app_settings DROP COLUMN chat_hibernation_orchestrator_idle_seconds;
ALTER TABLE app_settings DROP COLUMN chat_hibernation_worker_idle_seconds;
ALTER TABLE app_settings DROP COLUMN chat_hibernation;
