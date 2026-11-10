package controllers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
)

// OrchestratorResearchRequest is the body of POST /api/v1/orchestrators/{id}/research.
type OrchestratorResearchRequest struct {
	Enabled *bool `json:"enabled" description:"true switches research mode on, false off."`
}

// OrchestratorResearchResponse is an orchestrator's research mode (B413).
type OrchestratorResearchResponse struct {
	Active         bool   `json:"active" description:"Research mode is on and in effect."`
	Available      bool   `json:"available" description:"The orchestrator could switch research mode on."`
	WindowTokens   int    `json:"windowTokens" description:"The compaction window in effect, in tokens; 0 is the agent's own default."`
	ResearchWindow string `json:"researchWindow,omitempty" description:"The window research mode uses; empty when it is turned off for the project or globally."`
	Since          string `json:"since,omitempty" description:"When research mode was switched on (RFC 3339); empty when it is off."`
}

// researchModeService is the optional Chat service ability behind the route.
type researchModeService interface {
	SetResearchMode(ctx context.Context, id domain.SessionID, on bool) (chatsvc.ResearchStatus, error)
	ResearchStatus(ctx context.Context, id domain.SessionID) (chatsvc.ResearchStatus, bool)
}

func researchResponse(st chatsvc.ResearchStatus) OrchestratorResearchResponse {
	out := OrchestratorResearchResponse{Active: st.Active, Available: st.Available, WindowTokens: max(st.Window, 0), ResearchWindow: st.ResearchWindow}
	if st.Active && !st.Since.IsZero() {
		out.Since = st.Since.UTC().Format(time.RFC3339)
	}
	return out
}

// researchGuard limits an agent token to its own session, as the route rule
// does; it keeps the controller safe if a rule is ever loosened.
func researchGuard(w http.ResponseWriter, r *http.Request) bool {
	if agent, ok := scopedAgent(r); ok && (agent.Role != requestscope.RoleOrchestrator || agent.SessionID != orchestratorID(r)) {
		writeAgentScopeForbidden(w, r, "change the research mode of an orchestrator other than its own session")
		return false
	}
	return true
}

func (c *ConversationsController) researchService(w http.ResponseWriter, r *http.Request, method, path string) (researchModeService, bool) {
	svc, ok := c.Svc.(researchModeService)
	if c.Svc == nil || !ok {
		apispec.NotImplemented(w, r, method, path)
		return nil, false
	}
	return svc, true
}

func (c *ConversationsController) setOrchestratorResearch(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.researchService(w, r, "POST", "/api/v1/orchestrators/{id}/research")
	if !ok || !researchGuard(w, r) {
		return
	}
	var in OrchestratorResearchRequest
	if err := decodeJSON(r, &in); err != nil || in.Enabled == nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "RESEARCH_MODE_INVALID", "enabled must be true or false", nil)
		return
	}
	st, err := svc.SetResearchMode(r.Context(), orchestratorID(r), *in.Enabled)
	if err != nil {
		writeResearchError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, researchResponse(st))
}

func (c *ConversationsController) getOrchestratorResearch(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.researchService(w, r, "GET", "/api/v1/orchestrators/{id}/research")
	if !ok || !researchGuard(w, r) {
		return
	}
	st, found := svc.ResearchStatus(r.Context(), orchestratorID(r))
	if !found {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "ORCHESTRATOR_NOT_RUNNING", "no running orchestrator with this id", nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, researchResponse(st))
}

func writeResearchError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, chatsvc.ErrResearchUnsupported):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "RESEARCH_MODE_UNSUPPORTED", "research mode is not supported for this agent: only a Claude Code orchestrator can change its compaction window while it runs", nil)
	case errors.Is(err, chatsvc.ErrResearchDisabled):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "RESEARCH_MODE_DISABLED", "research mode is turned off: enable \"Dynamic orchestrator context\" in Settings, or set the project's orchestrator.researchCompactWindow to a size", nil)
	case errors.Is(err, chatsvc.ErrResearchNotApplied):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "RESEARCH_MODE_NOT_APPLIED", "research mode was not enabled: this agent keeps its compaction window until it restarts, and nothing is queued for the restart; the orchestrator stays in coordination mode, so delegate the reading to workers", nil)
	case errors.Is(err, chatsvc.ErrResearchRequestFailed):
		envelope.WriteAPIError(w, r, http.StatusBadGateway, "bad_gateway", "RESEARCH_MODE_REQUEST_FAILED", "research mode was not enabled: the request to change the agent's compaction window failed before it answered; try again", nil)
	default:
		writeConversationError(w, r, err)
	}
}
