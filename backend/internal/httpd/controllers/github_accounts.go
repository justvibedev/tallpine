package controllers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
)

// GitHubAccountItem is one signed-in account. It never carries a token.
type GitHubAccountItem struct {
	Login string `json:"login"`
	// CanPush is "yes", "no" or "unknown" for the project's repo; omitted when
	// no project was asked about.
	CanPush string `json:"canPush,omitempty" enum:"yes,no,unknown"`
}

// GitHubAccountsResponse lists the accounts signed in to github.com. Logins
// only: no token appears anywhere in it.
type GitHubAccountsResponse struct {
	Accounts []GitHubAccountItem `json:"accounts"`
	// Repo is owner/name of the project's GitHub repo ("" = none / not on github.com).
	Repo string `json:"repo,omitempty"`
	// Override is the project's pinned account ("" = auto-pick).
	Override string `json:"override,omitempty"`
	// AutoPick is the account new sessions get with no pin ("" = machine default).
	AutoPick string `json:"autoPick,omitempty"`
	// Unavailable is true when gh could not list accounts.
	Unavailable bool `json:"unavailable,omitempty"`
}

func (c *ProjectGitHubAccountController) registerAccounts(r chi.Router) {
	r.Get("/github/accounts", c.accounts)
	r.Get("/projects/{id}/github-accounts", c.accounts)
}

func (c *ProjectGitHubAccountController) accounts(w http.ResponseWriter, r *http.Request) {
	if c.Checker == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/github/accounts")
		return
	}
	res, found, err := c.Checker.Accounts(r.Context(), domain.ProjectID(chi.URLParam(r, "id")))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	if !found {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "PROJECT_NOT_FOUND", "project not found", nil)
		return
	}
	out := GitHubAccountsResponse{Accounts: make([]GitHubAccountItem, len(res.Accounts)), Repo: res.Repo,
		Override: res.Override, AutoPick: res.AutoPick, Unavailable: res.Unavailable}
	for i, a := range res.Accounts {
		out.Accounts[i] = GitHubAccountItem{Login: a.Login, CanPush: a.Push}
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}
