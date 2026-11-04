-- +goose Up
-- Two logins can share an email (a personal and a team organization), so an
-- account also keeps the organization it was added with, and the owner's own
-- label to tell entries apart.
ALTER TABLE agent_accounts ADD COLUMN org_id TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_accounts ADD COLUMN org_name TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_accounts ADD COLUMN label TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE agent_accounts DROP COLUMN label;
ALTER TABLE agent_accounts DROP COLUMN org_name;
ALTER TABLE agent_accounts DROP COLUMN org_id;
