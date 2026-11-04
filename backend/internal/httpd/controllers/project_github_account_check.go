package controllers

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ghaccount"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
)

// GitHubAccountChecker inspects the account a project's sessions would use.
type GitHubAccountChecker interface {
	CheckProject(ctx context.Context, id domain.ProjectID) (insp ghaccount.Inspection, repo, override string, found bool, err error)
}

// ProjectGitHubAccountCheckResponse says which GitHub login the project's token
// really is (the answer of `gh api user`). It never contains a token.
type ProjectGitHubAccountCheckResponse struct {
	Status string `json:"status" enum:"ok,no_account,no_token,mismatch,no_repo_access,unverified"`
	// Override is the project's explicit account setting ("" when unset).
	Override string `json:"override"`
	// Login is the account Tallpine picked for the project ("" = none).
	Login string `json:"login"`
	// TokenLogin is the login the token authenticates as ("" when unknown).
	TokenLogin string `json:"tokenLogin"`
	// Repo is owner/name of the project's GitHub origin ("" when none).
	Repo string `json:"repo"`
}

// ProjectGitHubAccountController serves the Settings account check.
type ProjectGitHubAccountController struct {
	Checker GitHubAccountChecker
}

// Register mounts the route.
func (c *ProjectGitHubAccountController) Register(r chi.Router) {
	r.Get("/projects/{id}/github-account-check", c.check)
}

func (c *ProjectGitHubAccountController) check(w http.ResponseWriter, r *http.Request) {
	if c.Checker == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/projects/{id}/github-account-check")
		return
	}
	insp, repo, override, found, err := c.Checker.CheckProject(r.Context(), domain.ProjectID(chi.URLParam(r, "id")))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	if !found {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "PROJECT_NOT_FOUND", "project not found", nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ProjectGitHubAccountCheckResponse{
		Status: insp.Status, Override: override, Login: insp.Login, TokenLogin: insp.TokenLogin, Repo: repo,
	})
}
