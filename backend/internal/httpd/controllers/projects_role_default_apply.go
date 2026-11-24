package controllers

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	projectsvc "tallpine.eu/tallpine/backend/internal/service/project"
)

// maxApplyRoleDefaultBody bounds the apply body (a list of project IDs).
const maxApplyRoleDefaultBody = 64 << 10

// RoleDefaultRoleParam is the {role} path parameter of the role-default routes.
type RoleDefaultRoleParam struct {
	Role string `path:"role" enum:"orchestrator,worker,reviewer" description:"Role whose app-wide default model and effort is compared or applied."`
}

// roleDefaultApplier is the project service's apply-to-all-projects ability
// for the app role defaults (B847).
type roleDefaultApplier interface {
	RoleDefaultDrift(ctx context.Context, role domain.DefaultRole) (projectsvc.RoleDefaultDriftReport, error)
	ApplyRoleDefault(ctx context.Context, role domain.DefaultRole, in projectsvc.ApplyRoleDefaultInput) (projectsvc.ApplyRoleDefaultResult, error)
}

func (c *ProjectsController) registerRoleDefaultApply(r chi.Router) {
	r.Get("/settings/role-defaults/{role}/drift", c.roleDefaultDrift)
	r.Post("/settings/role-defaults/{role}/apply", c.applyRoleDefault)
}

// roleDefaultApplierFor returns the applier, writing the refusal when the
// caller is an agent or the build has none.
func (c *ProjectsController) roleDefaultApplierFor(w http.ResponseWriter, r *http.Request, method, path string) (roleDefaultApplier, bool) {
	// App defaults are people's settings; an agent token neither reads the
	// comparison nor rewrites other projects through it.
	if _, scoped := scopedAgent(r); scoped {
		writeAgentScopeForbidden(w, r, "apply the app role defaults to projects")
		return nil, false
	}
	applier, ok := c.Mgr.(roleDefaultApplier)
	if c.Mgr == nil || !ok {
		apispec.NotImplemented(w, r, method, path)
		return nil, false
	}
	return applier, true
}

// roleDefaultDrift lists the projects whose role does not run with the app default.
func (c *ProjectsController) roleDefaultDrift(w http.ResponseWriter, r *http.Request) {
	applier, ok := c.roleDefaultApplierFor(w, r, "GET", "/api/v1/settings/role-defaults/{role}/drift")
	if !ok {
		return
	}
	report, err := applier.RoleDefaultDrift(r.Context(), domain.DefaultRole(chi.URLParam(r, "role")))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, report)
}

// applyRoleDefault brings the listed (or every drifting) project in line with
// the app default of the role.
func (c *ProjectsController) applyRoleDefault(w http.ResponseWriter, r *http.Request) {
	applier, ok := c.roleDefaultApplierFor(w, r, "POST", "/api/v1/settings/role-defaults/{role}/apply")
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxApplyRoleDefaultBody)
	var in projectsvc.ApplyRoleDefaultInput
	// An empty body applies to every drifting project.
	if err := decodeJSONStrict(r, &in); err != nil && !errors.Is(err, io.EOF) {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	in.Actor = "user"
	result, err := applier.ApplyRoleDefault(r.Context(), domain.DefaultRole(chi.URLParam(r, "role")), in)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, result)
}
