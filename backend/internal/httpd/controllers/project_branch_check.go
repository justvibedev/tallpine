package controllers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/branchcheck"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
)

// ProjectBranchSuggestion is a branch that is strictly ahead of the project's
// default branch and much newer, so sessions start from older code than the
// repository has. Nothing changes until the user switches the default.
type ProjectBranchSuggestion struct {
	Branch     string `json:"branch"`
	Ahead      int    `json:"ahead"`
	NewerDays  int    `json:"newerDays"`
	CheckedOut bool   `json:"checkedOut"`
}

// ProjectDefaultBranchCheckResponse is the default-branch sanity check of one
// project. Suggestion is absent when the default looks right.
type ProjectDefaultBranchCheckResponse struct {
	// DefaultBranch is the branch new session worktrees are cut from.
	DefaultBranch string                   `json:"defaultBranch"`
	Suggestion    *ProjectBranchSuggestion `json:"suggestion,omitempty"`
}

func (c *ProjectSkillsController) registerBranchCheck(r chi.Router) {
	r.Get("/projects/{id}/default-branch-check", c.branchCheck)
}

// branchCheck compares the branch sessions start from with the project
// folder's current branch and the usual default names. It only reads local git
// state.
func (c *ProjectSkillsController) branchCheck(w http.ResponseWriter, r *http.Request) {
	if c.Projects == nil || c.DefaultBranch == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/projects/{id}/default-branch-check")
		return
	}
	p, ok := c.scan(w, r)
	if !ok {
		return
	}
	resp := ProjectDefaultBranchCheckResponse{}
	t, ok := c.resolveTarget(r.Context(), p.path, p.baseBranch, false, nil)
	if !ok {
		envelope.WriteJSON(w, http.StatusOK, resp)
		return
	}
	resp.DefaultBranch = t.Branch
	if f, err := branchcheck.Detect(r.Context(), p.path, t.Remote, t.BaseRef, branchcheck.MinNewer); err == nil && f != nil {
		resp.Suggestion = &ProjectBranchSuggestion{Branch: f.Branch, Ahead: f.Ahead, NewerDays: f.NewerDays, CheckedOut: f.CheckedOut}
	}
	envelope.WriteJSON(w, http.StatusOK, resp)
}
