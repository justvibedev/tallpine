package specgen

import (
	"net/http"

	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	projectsvc "tallpine.eu/tallpine/backend/internal/service/project"
)

// projectOperations declares the canonical /projects operations. The set must
// stay 1:1 with the routes ProjectsController.Register mounts —
// TestRouteSpecParity fails the build otherwise.
func projectOperations() []operation {
	return []operation{
		{
			method: http.MethodGet, path: "/api/v1/projects", id: "listProjects", tag: "projects",
			summary: "List all registered projects (active + degraded)",
			resps: []respUnit{
				{http.StatusOK, controllers.ListProjectsResponse{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/projects", id: "addProject", tag: "projects",
			summary: "Register a new project from a git repository path",
			reqBody: projectsvc.AddInput{},
			resps: []respUnit{
				{http.StatusCreated, controllers.ProjectResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/projects/clone", id: "cloneProject", tag: "projects",
			summary: "Clone and register a project from a git repository URL",
			reqBody: projectsvc.CloneInput{},
			resps: []respUnit{
				{http.StatusCreated, controllers.ProjectResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusServiceUnavailable, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/projects/clone/check", id: "checkCloneDestination", tag: "projects",
			summary: "Check a clone destination and normalize its repository URL without network access",
			reqBody: projectsvc.CloneInput{},
			resps: []respUnit{
				{http.StatusOK, projectsvc.CloneDestination{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/projects/clone/prepare", id: "prepareCloneProject", tag: "projects",
			summary: "Clone a project without registering it so Git setup can be completed",
			reqBody: projectsvc.CloneInput{},
			resps: []respUnit{
				{http.StatusOK, projectsvc.ClonePreparationResult{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusServiceUnavailable, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/projects/clone/cleanup", id: "cleanupPreparedClone", tag: "projects",
			summary: "Remove an abandoned clone created for project preparation",
			reqBody: projectsvc.ClonePreparationCleanupInput{},
			resps: []respUnit{
				{http.StatusNoContent, nil},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/projects/initialize", id: "initializeProjectRepository", tag: "projects",
			summary: "Initialize a selected folder as a Git repository with an initial commit",
			reqBody: projectsvc.InitializeRepositoryInput{},
			resps: []respUnit{
				{http.StatusOK, projectsvc.InitializeRepositoryResult{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		}, {
			method: http.MethodGet, path: "/api/v1/projects/{id}", id: "getProject", tag: "projects",
			summary:    "Fetch one project; discriminates ok vs degraded",
			pathParams: []any{controllers.ProjectIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.GetProjectResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPut, path: "/api/v1/projects/{id}", id: "updateProjectSettings", tag: "projects",
			summary:    "Atomically replace a project's display name and config",
			pathParams: []any{controllers.ProjectIDParam{}},
			reqBody:    projectsvc.UpdateSettingsInput{},
			resps: []respUnit{
				{http.StatusOK, controllers.ProjectResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPut, path: "/api/v1/projects/{id}/config", id: "setProjectConfig", tag: "projects",
			summary:    "Replace a project's per-project config",
			pathParams: []any{controllers.ProjectIDParam{}},
			reqBody:    projectsvc.SetConfigInput{},
			resps: []respUnit{
				{http.StatusOK, controllers.ProjectResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPatch, path: "/api/v1/projects/{id}/role-settings", id: "setProjectRoleSettings", tag: "projects",
			summary:    "Set only the agent, model and effort of a project's orchestrator, worker and reviewer roles",
			pathParams: []any{controllers.ProjectIDParam{}},
			reqBody:    projectsvc.SetRoleSettingsInput{},
			resps: []respUnit{
				{http.StatusOK, controllers.ProjectResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusForbidden, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPut, path: "/api/v1/projects/{id}/rules", id: "setProjectRules", tag: "projects",
			summary:    "Set only a project's agentRules and orchestratorRules",
			pathParams: []any{controllers.ProjectIDParam{}},
			reqBody:    projectsvc.SetRulesInput{},
			resps: []respUnit{
				{http.StatusOK, controllers.ProjectResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusForbidden, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPatch, path: "/api/v1/projects/{id}/permissions", id: "setProjectPermissions", tag: "projects",
			summary:    "Remember project permissions for future sessions",
			pathParams: []any{controllers.ProjectIDParam{}},
			reqBody:    projectsvc.SetPermissionsInput{},
			resps: []respUnit{
				{http.StatusOK, controllers.ProjectResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodDelete, path: "/api/v1/projects/{id}/permissions", id: "forgetProjectPermissions", tag: "projects",
			summary:    "Forget remembered project permissions",
			pathParams: []any{controllers.ProjectIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.ProjectResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodDelete, path: "/api/v1/projects/{id}", id: "removeProject", tag: "projects",
			summary:    "Remove a project; stops sessions, cleans workspaces, unregisters",
			pathParams: []any{controllers.ProjectIDParam{}},
			resps: []respUnit{
				{http.StatusOK, projectsvc.RemoveResult{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/mobile/feature", id: "setMobileFeature", tag: "mobile",
			summary: "Turn the mobile feature on or off (off by default)",
			reqBody: controllers.SetMobileFeatureRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.MobileStatusResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/mobile/public-url", id: "setMobilePublicBaseUrl", tag: "mobile",
			summary: "Set or clear the reachable base URL that pairing codes advertise",
			reqBody: controllers.SetMobilePublicBaseURLRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.MobileStatusResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/mobile/quick-tunnel", id: "setMobileQuickTunnel", tag: "mobile",
			summary: "Opt in or out of a Cloudflare quick tunnel (Cloudflare terminates TLS)",
			reqBody: controllers.SetMobileQuickTunnelRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.MobileStatusResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
	}
}
