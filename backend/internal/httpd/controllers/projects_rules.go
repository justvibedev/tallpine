package controllers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	projectsvc "tallpine.eu/tallpine/backend/internal/service/project"
)

// maxRulesBody bounds the PUT /projects/{id}/rules body.
const maxRulesBody = 64 << 10

// registerRules mounts the narrow rules route.
func (c *ProjectsController) registerRules(r chi.Router) {
	r.Put("/projects/{id}/rules", c.setRules)
}

// setRules changes only a project's agentRules and orchestratorRules. The
// body decodes strictly, so any other config field is a 400 rather than a
// silent write; this is the narrow path scoped orchestrator tokens use on
// their own project.
func (c *ProjectsController) setRules(w http.ResponseWriter, r *http.Request) {
	if c.Mgr == nil {
		apispec.NotImplemented(w, r, "PUT", "/api/v1/projects/{id}/rules")
		return
	}
	// Two capped text fields fit well inside this; a larger body is refused
	// before it is read in full.
	r.Body = http.MaxBytesReader(w, r.Body, maxRulesBody)
	var in projectsvc.SetRulesInput
	if err := decodeJSONStrict(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	in.Actor = "user"
	if agent, ok := scopedAgent(r); ok {
		in.Actor = string(agent.Role) + ":" + string(agent.SessionID)
		if !c.agentSettingsAllowed(w, r, projectID(r), rulesKeys(in)) {
			return
		}
	}
	p, err := c.Mgr.SetRules(r.Context(), projectID(r), in)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ProjectResponse{Project: p})
}
