package controllers

import (
	"context"
	"net/http"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
)

// PolicyDecider decides the agent policy for a session; the policy engine
// satisfies it.
type PolicyDecider interface {
	Decide(ctx context.Context, session agentpolicy.SessionRef) agentpolicy.Decision
}

// refuseCappedAgentPreviewStart blocks agent-scoped preview starts while the
// calling session's policy forbids bypass. launch.json runs a package script or
// workspace file the agent can edit, so no content hash can make an agent start
// safe. Human starts and uncapped agents run as before. It returns true after
// writing the refusal.
func (c *SessionsController) refuseCappedAgentPreviewStart(w http.ResponseWriter, r *http.Request) bool {
	agent, ok := requestscope.AgentFrom(r.Context())
	if !ok || c.Policy == nil {
		return false
	}
	decision := c.Policy.Decide(r.Context(), agentpolicy.SessionRef{ID: agent.SessionID, Project: agent.ProjectID})
	if !permissioncap.Of(decision).ForbidsBypass() {
		return false
	}
	envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", "PREVIEW_AGENT_START_FORBIDDEN",
		"A permission cap is active, so agents cannot start preview servers. Ask the user to start the preview from Tallpine.", nil)
	return true
}
