package controllers

import (
	"context"
	"errors"
	"io/fs"
	"net/http"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/skillbridge"
)

// DefaultBranchResolver resolves the ref new session worktrees are cut from.
// The git workspace adapter satisfies it.
type DefaultBranchResolver interface {
	ResolveDefaultBranch(ctx context.Context, repoPath, configuredBranch string) (ports.WorkspaceDefaultBranch, error)
	FetchDefaultBranch(ctx context.Context, repoPath string, target ports.WorkspaceDefaultBranch, gitEnv []string) error
}

// ProjectSkillCommitTarget is the branch new sessions start from. NeedsPush is
// true when they start from the remote-tracking ref, so committing must also
// push to Remote/Branch.
type ProjectSkillCommitTarget struct {
	Remote    string `json:"remote"`
	Branch    string `json:"branch"`
	NeedsPush bool   `json:"needsPush"`
}

// CommitProjectSkillsRequest names the created paths to commit. Each must be a
// pending instruction file or skill directory of the checkout.
type CommitProjectSkillsRequest struct {
	Paths []string `json:"paths"`
}

// CommitProjectSkillsResponse reports the commit. When the push was refused,
// Pushed is false, PushError holds git's message and the commit is kept
// locally (on Branch, or on SideBranch when Branch could not take it).
type CommitProjectSkillsResponse struct {
	SHA          string `json:"sha"`
	Remote       string `json:"remote"`
	Branch       string `json:"branch"`
	NeedsPush    bool   `json:"needsPush"`
	Pushed       bool   `json:"pushed"`
	PushError    string `json:"pushError,omitempty"`
	LocalUpdated bool   `json:"localUpdated"`
	SideBranch   string `json:"sideBranch,omitempty"`
}

// PushProjectSkillsBranchRequest is a commit made by the commit route, to push
// as a new branch for a pull request.
type PushProjectSkillsBranchRequest struct {
	SHA string `json:"sha"`
}

// PushProjectSkillsBranchResponse is the remote branch the commit was pushed to.
type PushProjectSkillsBranchResponse struct {
	Remote string `json:"remote"`
	Branch string `json:"branch"`
}

func (c *ProjectSkillsController) resolveTarget(ctx context.Context, path, configured string, refresh bool, gitEnv []string) (skillbridge.CommitTarget, bool) {
	if c.DefaultBranch == nil || path == "" {
		return skillbridge.CommitTarget{}, false
	}
	t, err := c.DefaultBranch.ResolveDefaultBranch(ctx, path, configured)
	if err != nil || t.Branch == "" || t.BaseRef == "" {
		return skillbridge.CommitTarget{}, false
	}
	if refresh {
		// Like spawn: refresh the tracking ref so the commit is built on the
		// current remote head. A failed fetch falls back to the cached ref.
		_ = c.DefaultBranch.FetchDefaultBranch(ctx, path, t, gitEnv)
	}
	return skillbridge.CommitTarget{Remote: t.Remote, Branch: t.Branch, BaseRef: t.BaseRef, PushEnv: gitEnv}, true
}

// projectGitEnv runs one git process as the project's GitHub account; nil when
// the project has none or the controller is not wired for it.
func (c *ProjectSkillsController) projectGitEnv(ctx context.Context, id domain.ProjectID) []string {
	if c.GitCredentials == nil {
		return nil
	}
	return c.GitCredentials.ProjectGitEnv(ctx, id)
}

func (c *ProjectSkillsController) commitTarget(ctx context.Context, path, configured string) *ProjectSkillCommitTarget {
	t, ok := c.resolveTarget(ctx, path, configured, false, nil)
	if !ok {
		return nil
	}
	return &ProjectSkillCommitTarget{Remote: t.Remote, Branch: t.Branch, NeedsPush: t.NeedsPush()}
}

// commit commits the named pending paths to the branch sessions start from,
// and pushes when sessions start from the remote. It is only ever called by an
// explicit user action.
func (c *ProjectSkillsController) commit(w http.ResponseWriter, r *http.Request) {
	if c.Projects == nil || c.DefaultBranch == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/projects/{id}/skills/commit")
		return
	}
	var in CommitProjectSkillsRequest
	if err := decodeJSONStrict(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "SKILLS_COMMIT_INVALID", "invalid request body", nil)
		return
	}
	p, ok := c.scan(w, r)
	if !ok {
		return
	}
	target, ok := c.resolveTarget(r.Context(), p.path, p.baseBranch, true, c.projectGitEnv(r.Context(), p.id))
	if !ok {
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "DEFAULT_BRANCH_UNRESOLVED", "the branch new sessions start from could not be resolved", nil)
		return
	}
	opts := skillbridge.OptionsFor("", p.path, p.env)
	opts.Committed = skillbridge.GitCommitted(p.path)
	pending := skillbridge.Discover(opts).PendingCommit
	res, err := skillbridge.CommitPaths(r.Context(), p.path, target, in.Paths, pending, skillbridge.CommitMessage)
	switch {
	case errors.Is(err, skillbridge.ErrPathNotAllowed), errors.Is(err, skillbridge.ErrNothingToCommit):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "SKILLS_COMMIT_INVALID", err.Error(), nil)
		return
	case errors.Is(err, skillbridge.ErrPathExists):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "SKILLS_COMMIT_CONFLICT", err.Error(), nil)
		return
	case err != nil && res.SHA == "":
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "SKILLS_COMMIT_FAILED", err.Error(), nil)
		return
	case err != nil:
		// The commit exists but a follow-up step failed: say so, with the id.
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "SKILLS_COMMIT_PARTIAL", err.Error(), map[string]any{"sha": res.SHA})
		return
	}
	envelope.WriteJSON(w, http.StatusOK, CommitProjectSkillsResponse{
		SHA: res.SHA, Remote: target.Remote, Branch: target.Branch, NeedsPush: target.NeedsPush(),
		Pushed: res.Pushed, PushError: res.PushError, LocalUpdated: res.LocalUpdated, SideBranch: res.SideBranch,
	})
}

// pushBranch pushes a commit as a new branch on the project's remote, the
// fallback when the default branch refuses direct pushes. Never forces.
func (c *ProjectSkillsController) pushBranch(w http.ResponseWriter, r *http.Request) {
	if c.Projects == nil || c.DefaultBranch == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/projects/{id}/skills/push-branch")
		return
	}
	var in PushProjectSkillsBranchRequest
	if err := decodeJSONStrict(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "SKILLS_COMMIT_INVALID", "invalid request body", nil)
		return
	}
	p, ok := c.scan(w, r)
	if !ok {
		return
	}
	gitEnv := c.projectGitEnv(r.Context(), p.id)
	target, ok := c.resolveTarget(r.Context(), p.path, p.baseBranch, false, gitEnv)
	if !ok || !target.NeedsPush() {
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "DEFAULT_BRANCH_UNRESOLVED", "the project has no remote to push to", nil)
		return
	}
	branch, err := skillbridge.PushAsBranch(r.Context(), p.path, target.Remote, in.SHA, gitEnv)
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadGateway, "bad_gateway", "SKILLS_PUSH_FAILED", err.Error(), nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, PushProjectSkillsBranchResponse{Remote: target.Remote, Branch: branch})
}

// RemoveProjectSkillRequest names a bridged skill folder of the checkout, for
// example ".claude/skills/qa".
type RemoveProjectSkillRequest struct {
	Path string `json:"path"`
}

// RemoveProjectSkillResponse reports what a removal did. LocalOnly is true when
// the path is still on the branch new sessions start from (Branch): the
// deletion exists only in the checkout until someone commits it, and sessions
// keep the link.
type RemoveProjectSkillResponse struct {
	LocalOnly bool   `json:"localOnly"`
	Branch    string `json:"branch,omitempty"`
}

// remove deletes a link or copy the bridge created. The skill it points to is
// never touched, and anything that is not a bridge product is refused.
func (c *ProjectSkillsController) remove(w http.ResponseWriter, r *http.Request) {
	if c.Projects == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/projects/{id}/skills/remove")
		return
	}
	var in RemoveProjectSkillRequest
	if err := decodeJSONStrict(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "SKILL_REMOVE_INVALID", "invalid request body", nil)
		return
	}
	p, ok := c.scan(w, r)
	if !ok {
		return
	}
	var resp RemoveProjectSkillResponse
	if t, ok := c.resolveTarget(r.Context(), p.path, p.baseBranch, false, nil); ok && skillbridge.TrackedAt(r.Context(), p.path, t.BaseRef, in.Path) {
		resp = RemoveProjectSkillResponse{LocalOnly: true, Branch: t.Branch}
	}
	switch err := skillbridge.RemoveBridged(p.path, in.Path); {
	case errors.Is(err, skillbridge.ErrNotBridged):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "SKILL_NOT_BRIDGED", err.Error(), nil)
	case errors.Is(err, fs.ErrNotExist):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "SKILL_NOT_FOUND", "that skill folder no longer exists", nil)
	case err != nil:
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "SKILL_REMOVE_FAILED", err.Error(), nil)
	default:
		envelope.WriteJSON(w, http.StatusOK, resp)
	}
}
