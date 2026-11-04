-- Enterprise agent policy, user layer. See agentpolicy.Resolver.

-- name: GetAgentPolicy :one
SELECT agent_policy FROM app_settings WHERE id = 1;

-- name: SetAgentPolicy :exec
UPDATE app_settings SET agent_policy = ?, updated_at = ? WHERE id = 1;
