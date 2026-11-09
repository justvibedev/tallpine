package ports

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// WorkspaceDefaultBranchRefresher is an optional capability for Git-backed
// workspaces. Resolution is local-only so callers can retain the canonical ref
// even when the subsequent best-effort network refresh fails.
type WorkspaceDefaultBranchRefresher interface {
	ResolveDefaultBranch(ctx context.Context, repoPath, configuredBranch string) (WorkspaceDefaultBranch, error)
	// FetchDefaultBranch refreshes target. gitEnv (from ProjectGitCredentials,
	// may be nil) is added to the git process's environment only.
	FetchDefaultBranch(ctx context.Context, repoPath string, target WorkspaceDefaultBranch, gitEnv []string) error
}

// ProjectGitCredentials gives daemon-run git commands the project's GitHub
// account, so they do not depend on which account the machine's gh has active.
type ProjectGitCredentials interface {
	// ProjectGitEnv returns environment entries for one git process, or nil when
	// the project has no credential of its own. The entries hold a secret: pass
	// them only to that process and never log them.
	ProjectGitEnv(ctx context.Context, project domain.ProjectID) []string
}

// WorkspaceDefaultBranch is a locally resolved default-branch target. BaseRef
// is canonical (for example refs/remotes/upstream/main), so materialization
// never has to reconstruct the remote decision from an ambiguous slash.
type WorkspaceDefaultBranch struct {
	Remote  string
	Branch  string
	BaseRef string
}
