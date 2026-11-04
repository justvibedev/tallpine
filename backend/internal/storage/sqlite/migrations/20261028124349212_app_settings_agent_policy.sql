-- Summary: enterprise agent policy, user layer (JSON; '{}' = defaults, no restriction).
-- +goose Up
-- The managed file and TALLPINE_AGENT_* environment override this layer; see docs/agent-policy.md.
ALTER TABLE app_settings ADD COLUMN agent_policy TEXT NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE app_settings DROP COLUMN agent_policy;
