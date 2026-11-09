package controllers

import (
	"context"
	"net/http"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/protectstate"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
	"tallpine.eu/tallpine/backend/internal/previewserver"
)

// PolicyDecider decides the agent policy for a session; the policy engine
// satisfies it.
type PolicyDecider interface {
	Decide(ctx context.Context, session agentpolicy.SessionRef) agentpolicy.Decision
}

// refuseAgentPreviewStart blocks agent-scoped preview starts while the calling
// session's policy forbids bypass or protects Tallpine state. launch.json runs a
// package script or workspace file the agent can edit, so no content hash can
// make an agent start safe, and on a host with no seatbelt the server would run
// outside the fence. Human starts and unrestricted agents run as before. It
// returns true after writing the refusal.
func (c *SessionsController) refuseAgentPreviewStart(w http.ResponseWriter, r *http.Request) bool {
	agent, ok := requestscope.AgentFrom(r.Context())
	if !ok || c.Policy == nil {
		return false
	}
	decision := c.Policy.Decide(r.Context(), agentpolicy.SessionRef{ID: agent.SessionID, Project: agent.ProjectID})
	var reason string
	switch {
	case permissioncap.Of(decision).ForbidsBypass():
		reason = "A permission cap is active"
	case protectstate.Of(decision).Enabled():
		reason = "Security mode is on"
	default:
		return false
	}
	envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", "PREVIEW_AGENT_START_FORBIDDEN",
		reason+", so agents cannot start preview servers. Ask the user to start the preview from Tallpine.", nil)
	return true
}

// previewFence is the Security mode input for a session's preview start,
// decided for the session rather than the caller: a human start runs launch.json
// the session's agent could have written.
func (c *SessionsController) previewFence(ctx context.Context, id domain.SessionID, project domain.ProjectID) previewserver.Fence {
	if c.Policy == nil {
		return previewserver.Fence{}
	}
	return previewserver.Fence{
		Decision:  c.Policy.Decide(ctx, agentpolicy.SessionRef{ID: id, Project: project}),
		RunFile:   c.RunFilePath,
		DataDir:   c.DataDir,
		SessionID: id,
	}
}
