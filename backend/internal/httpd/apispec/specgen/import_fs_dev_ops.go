package specgen

import (
	"net/http"

	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	importsvc "tallpine.eu/tallpine/backend/internal/service/importer"
)

// importOperations declares the /import operations. Must stay 1:1 with
// the routes ImportController.Register mounts (enforced by the parity test).
func importOperations() []operation {
	return []operation{
		{
			method: http.MethodPost, path: "/api/v1/imports/validate", id: "validateImport", tag: "import",
			summary: "Validate a selected folder for project import onboarding",
			reqBody: importsvc.ImportValidationInput{},
			resps: []respUnit{
				{http.StatusOK, importsvc.ImportValidationResult{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/imports/ao", id: "runAOImport", tag: "import",
			summary: "Plan or run the read-only migration of projects and settings from an official Agent Orchestrator install",
			reqBody: controllers.AOImportRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.AOImportResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/imports/prepare-git", id: "prepareImportGit", tag: "import",
			summary: "Run approved Git preparation actions for project import onboarding",
			reqBody: importsvc.GitPreparationInput{},
			resps: []respUnit{
				{http.StatusOK, importsvc.GitPreparationResult{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
	}
}

// fsOperations declares the read-only filesystem-browsing operations. Must stay
// 1:1 with the routes FSController.Register mounts (enforced by the parity test).
func fsOperations() []operation {
	return []operation{
		{
			method: http.MethodGet, path: "/api/v1/fs/dirs", id: "listDirs", tag: "fs",
			summary:    "List the subdirectories of a directory on the daemon host",
			pathParams: []any{controllers.ListDirsQuery{}},
			resps: []respUnit{
				{http.StatusOK, controllers.ListDirsResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusForbidden, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
	}
}

// devOperations declares developer-only API operations. Must stay 1:1 with
// the routes DevController.Register mounts (enforced by the parity test).
func devOperations() []operation {
	return []operation{
		{
			method: http.MethodPost, path: "/api/v1/dev/import-projects", id: "runDevImportProjects", tag: "dev",
			summary: "Run the developer project-registry import through the daemon store",
			reqBody: controllers.DevImportProjectsRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.DevImportProjectsResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
	}
}
