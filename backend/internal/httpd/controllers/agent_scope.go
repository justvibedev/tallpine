package controllers

import (
	"context"
	"net/http"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
)

// scopedAgent returns the agent principal of a request made with a scoped
// agent token (TALLPINE_SESSION_TOKEN). The daemon's auth middleware has already
// verified the token and the route; handlers whose scope lives in the request
// body or query enforce the rest with it. Install-token and LAN callers have
// none and keep full access.
func scopedAgent(r *http.Request) (requestscope.Agent, bool) {
	return requestscope.AgentFrom(r.Context())
}

// writeAgentScopeForbidden answers a scoped agent's request that the route
// allows but whose body or query reaches outside the agent's scope.
func writeAgentScopeForbidden(w http.ResponseWriter, r *http.Request, reason string) {
	envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", "AGENT_SCOPE_FORBIDDEN",
		"An agent session token cannot "+reason, nil)
}

// scopeSessionListProject is the project a scoped agent's session list covers.
// An agent lists its own project only; visibleToAgent then keeps what its role
// may read. Asking for another project is refused, never silently rewritten to
// the caller's own, except that an orchestrator may list an eval project its
// project created (requestscope.Agent.EvalAccess).
func scopeSessionListProject(ctx context.Context, agent requestscope.Agent, asked domain.ProjectID) (project domain.ProjectID, allowed bool) {
	switch {
	case asked != "" && agent.MayActOn(asked):
		return asked, true
	case asked == "" || asked == agent.ProjectID:
		return agent.ProjectID, true
	case agent.EvalAccess(ctx, asked):
		return asked, true
	}
	return "", false
}

// visibleToAgent keeps the sessions a scoped agent may read; every session for
// any other caller.
func visibleToAgent(r *http.Request, sessions []domain.Session) []domain.Session {
	agent, ok := scopedAgent(r)
	if !ok {
		return sessions
	}
	visible := sessions[:0:0]
	for _, sess := range sessions {
		if agent.CanSee(sess.ID, sess.ProjectID, sess.Kind) || agent.EvalAccess(r.Context(), sess.ProjectID) {
			visible = append(visible, sess)
		}
	}
	return visible
}
