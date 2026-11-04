package gitworktree

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/gitdefault"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// NewDefaultBranchResolver returns the default-branch resolution session spawn
// uses, without a managed worktree root, for callers that only need to know
// which ref new session worktrees are cut from (for example committing agent
// instruction files to that ref). Binary falls back to git from PATH.
func NewDefaultBranchResolver(binary string) ports.WorkspaceDefaultBranchRefresher {
	if binary == "" {
		binary = defaultGitBinary
	}
	return &Workspace{binary: binary, run: runCommand}
}

// resolveAnnounced resolves the repository default for a spawn and tells the
// user (via the injected logger) when it fell back to the local HEAD branch.
func (w *Workspace) resolveAnnounced(ctx, remoteCtx context.Context, repo string) (gitdefault.Resolution, error) {
	resolution, err := gitdefault.New(w.binary, gitdefault.Runner(w.run)).Resolve(ctx, remoteCtx, repo)
	if err == nil && resolution.Source == gitdefault.SourceLocalHead {
		w.logf("gitworktree: %s", gitdefault.LocalHeadNote(repo, resolution))
	}
	return resolution, err
}
