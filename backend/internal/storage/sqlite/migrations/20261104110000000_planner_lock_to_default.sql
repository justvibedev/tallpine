-- +goose Up
-- The user layer's planner.mode and planner.autoApprove become managed-only and
-- the app-wide workflow defaults carry the user's choice (B525). Move the stored
-- values so no install behaves differently, and never start auto-approving plans
-- anywhere. Only a version 2 user document is touched; an invalid or flat v1
-- document is left alone. Every step is guarded by the entry it consumes, and the
-- last step removes the entries, so a rerun changes nothing.
--
-- Per-project user overrides of these two fields cannot exist (the fields are
-- not project-overridable), so only the global rules.planner entries are moved.

-- A user autoApprove of "off" locked plan auto-approval off everywhere: pin
-- every project that turned it on to an explicit false so it stays off now that
-- the lock no longer applies.
UPDATE projects
SET config = json_set(config, '$.planAutoApprove', json('false'))
WHERE config IS NOT NULL AND json_valid(config) AND json_type(config) = 'object'
  AND json_extract(config, '$.planAutoApprove') = 1
  AND (SELECT CASE WHEN json_valid(agent_policy) AND json_type(agent_policy) = 'object'
                   THEN json_type(agent_policy, '$.version') IS NOT NULL
                        AND json_extract(agent_policy, '$.rules.planner.autoApprove') = 'off'
                   ELSE 0 END
       FROM app_settings WHERE id = 1);

-- The same lock also closed the eval waiver (an orchestrator approving plans
-- without the owner) in every project, present and future. Keep that as a lasting
-- install-wide gate: workflow_defaults.evalWaiverClosed, checked by the daemon
-- next to the managed lock. eval itself is left alone.
UPDATE app_settings
SET workflow_defaults = json_set(workflow_defaults, '$.evalWaiverClosed', json('true'))
WHERE id = 1 AND json_valid(workflow_defaults)
  AND CASE WHEN json_valid(agent_policy) AND json_type(agent_policy) = 'object'
           THEN json_type(agent_policy, '$.version') IS NOT NULL
                AND json_extract(agent_policy, '$.rules.planner.autoApprove') = 'off'
           ELSE 0 END;

-- A user planner.mode becomes the app default planning mode. Projects keep
-- their own values.
UPDATE app_settings
SET workflow_defaults = json_set(workflow_defaults, '$.planningMode', json_extract(agent_policy, '$.rules.planner.mode'))
WHERE id = 1 AND json_valid(workflow_defaults)
  AND CASE WHEN json_valid(agent_policy) AND json_type(agent_policy) = 'object'
           THEN json_type(agent_policy, '$.version') IS NOT NULL
                AND json_extract(agent_policy, '$.rules.planner.mode') IN ('auto', 'always', 'off')
           ELSE 0 END;

-- Drop the consumed entries from the user document.
UPDATE app_settings
SET agent_policy = json_remove(agent_policy, '$.rules.planner.mode', '$.rules.planner.autoApprove')
WHERE id = 1
  AND CASE WHEN json_valid(agent_policy) AND json_type(agent_policy) = 'object'
           THEN json_type(agent_policy, '$.version') IS NOT NULL
                AND json_type(agent_policy, '$.rules.planner') = 'object'
           ELSE 0 END;

-- +goose Down
SELECT 1;
