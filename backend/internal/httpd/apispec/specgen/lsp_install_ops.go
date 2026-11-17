package specgen

import (
	"net/http"

	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
)

// lspInstallOperations run the fixed install commands of the LSP catalog
// (B781): the Claude plugin or the language server, picked by id only.
func lspInstallOperations() []operation {
	return []operation{
		{
			method: http.MethodGet, path: "/api/v1/projects/{id}/skills/lsp/install", id: "getProjectLSPInstall", tag: "projects",
			pathParams: []any{controllers.ProjectIDParam{}},
			summary:    "Read the project's current or last LSP install job, with its output tail",
			resps:      []respUnit{{http.StatusOK, controllers.ProjectLSPInstallStatus{}}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodPost, path: "/api/v1/projects/{id}/skills/lsp/install", id: "startProjectLSPInstall", tag: "projects",
			pathParams: []any{controllers.ProjectIDParam{}}, reqBody: controllers.ProjectLSPInstallRequest{},
			summary: "Install an LSP plugin or language server by catalog id; runs only the daemon's fixed commands, one install at a time per project",
			resps: []respUnit{
				{http.StatusAccepted, controllers.ProjectLSPInstallStatus{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
	}
}
