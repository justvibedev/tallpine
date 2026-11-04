-- +goose Up
-- Codex accounts used to live only in a file registry (account.json per
-- account). agent_accounts becomes their one stored model, so it also keeps the
-- registry descriptor fields. All are empty for Claude accounts and for Codex
-- accounts added before the registry import; verified_at is NULL until the
-- provider confirmed the login.
ALTER TABLE agent_accounts ADD COLUMN source TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_accounts ADD COLUMN auth_method TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_accounts ADD COLUMN provider_account_id TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_accounts ADD COLUMN verified_at TIMESTAMP;

-- +goose Down
ALTER TABLE agent_accounts DROP COLUMN verified_at;
ALTER TABLE agent_accounts DROP COLUMN provider_account_id;
ALTER TABLE agent_accounts DROP COLUMN auth_method;
ALTER TABLE agent_accounts DROP COLUMN source;
