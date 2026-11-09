package specgen

import (
	"net/http"

	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/service/githubpat"
)

// prOperations declares the PR action operations. These live in the SCM lane:
// the handler delegates to a PRService backed by the SCM provider. A nil
// PRService (SCM not configured) returns 501 for both routes.
func prOperations() []operation {
	return []operation{
		{
			method: http.MethodPost, path: "/api/v1/prs/{id}/merge", id: "mergePR", tag: "prs",
			summary:    "Squash-merge a pull request",
			pathParams: []any{controllers.PRIDParam{}},
			reqBody:    controllers.MergePRRequest{},
			resps: []respUnit{
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusOK, controllers.MergePRResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusUnprocessableEntity, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/prs/{id}/merge-preflight", id: "mergePRPreflight", tag: "prs",
			summary:    "Check every merge precondition without merging",
			pathParams: []any{controllers.PRIDParam{}},
			reqBody:    controllers.MergePRRequest{},
			resps: []respUnit{
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusOK, controllers.MergePreflightResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusUnprocessableEntity, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/prs/{id}/resolve-comments", id: "resolveComments", tag: "prs",
			summary:    "Resolve review threads on a pull request",
			pathParams: []any{controllers.PRIDParam{}},
			reqBody:    nil, // body is optional: omitting it resolves all unresolved threads
			resps: []respUnit{
				{http.StatusOK, controllers.ResolveCommentsResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusUnprocessableEntity, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		// GitHub PAT + repos (local daemon storage)
		{
			method: http.MethodPut, path: "/api/v1/github/pat", id: "putGitHubPAT", tag: "github",
			summary: "Store a GitHub personal access token locally",
			reqBody: controllers.PutGitHubPATRequest{},
			resps:   []respUnit{{http.StatusOK, map[string]string{"status": "ok"}}, {http.StatusBadRequest, envelope.APIError{}}, {http.StatusInternalServerError, envelope.APIError{}}},
		},
		{
			method: http.MethodDelete, path: "/api/v1/github/pat", id: "deleteGitHubPAT", tag: "github",
			summary: "Remove the stored GitHub personal access token",
			resps:   []respUnit{{http.StatusOK, map[string]string{"status": "ok"}}, {http.StatusInternalServerError, envelope.APIError{}}},
		},
		{
			method: http.MethodGet, path: "/api/v1/github/status", id: "getGitHubStatus", tag: "github",
			summary: "Report whether a GitHub token is stored and the GitHub username Tallpine is signed in as",
			resps:   []respUnit{{http.StatusOK, controllers.GitHubStatusResponse{}}, {http.StatusInternalServerError, envelope.APIError{}}},
		},
		{
			method: http.MethodGet, path: "/api/v1/github/repos", id: "listGitHubRepos", tag: "github",
			summary: "List repositories accessible with the stored GitHub token",
			resps:   []respUnit{{http.StatusOK, map[string]any{"repos": []githubpat.Repo{}}}, {http.StatusUnauthorized, envelope.APIError{}}, {http.StatusInternalServerError, envelope.APIError{}}},
		},
	}
}
