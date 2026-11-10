package controllers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	projectsvc "tallpine.eu/tallpine/backend/internal/service/project"
)

// maxRoleSettingsBody bounds the PATCH /projects/{id}/role-settings body.
const maxRoleSettingsBody = 8 << 10

func (c *ProjectsController) registerRoleSettings(r chi.Router) {
	r.Patch("/projects/{id}/role-settings", c.setRoleSettings)
}

// setRoleSettings changes only the agent, model and effort of a project's
// orchestrator, worker and reviewer roles. It is the one project config write
// the phone may make (mobile in lan_route_policy.go); the body decodes
// strictly, so env, postCreate or any other config field is a 400. Agent
// session tokens are refused: the route is not in the agent scope table.
func (c *ProjectsController) setRoleSettings(w http.ResponseWriter, r *http.Request) {
	if c.Mgr == nil {
		apispec.NotImplemented(w, r, "PATCH", "/api/v1/projects/{id}/role-settings")
		return
	}
	if _, ok := scopedAgent(r); ok {
		writeAgentScopeForbidden(w, r, "change a project's role settings")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRoleSettingsBody)
	var in projectsvc.SetRoleSettingsInput
	if err := decodeJSONStrict(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	in.Actor = "user"
	p, err := c.Mgr.SetRoleSettings(r.Context(), projectID(r), in)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ProjectResponse{Project: p})
}
