package gitworktree

import (
	"context"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// orchestratorRefreshTimeout bounds the whole best-effort refresh so a slow
// remote never holds up an orchestrator spawn or restore.
var orchestratorRefreshTimeout = 60 * time.Second

// refreshOrchestratorWorktree fast-forwards a reused orchestrator worktree to
// the project's default branch (B190). Orchestrator worktrees live at a fixed
// path and are reused across spawns, restores and app restarts, so without
// this they drift hundreds of commits behind and orchestrators run stale repo
// scripts and read stale docs. Workers get a fresh worktree per session and
// are never touched.
//
// Callers opt in with cfg.RefreshOrchestrator only when no agent is attached.
//
// Best effort only: it never returns an error. A failed fetch still
// fast-forwards to the locally known remote-tracking ref. A dirty worktree,
// or one with commits not on the default branch, is left alone and the reason
// is logged.
func (w *Workspace) refreshOrchestratorWorktree(ctx context.Context, cfg ports.WorkspaceConfig, repo, path string) {
	if cfg.Kind != domain.KindOrchestrator || !cfg.RefreshOrchestrator {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), orchestratorRefreshTimeout)
	defer cancel()
	target, err := w.ResolveDefaultBranch(ctx, repo, cfg.BaseBranch)
	if err != nil {
		w.logf("gitworktree: orchestrator refresh skipped for %s: resolve default branch: %v", path, err)
		return
	}
	if err := w.FetchDefaultBranch(ctx, repo, target); err != nil {
		w.logf("gitworktree: orchestrator refresh for %s: fetch failed, using local %s: %v", path, target.BaseRef, err)
	}
	status, err := w.run(ctx, w.binary, "-C", path, "status", "--porcelain")
	if err != nil {
		w.logf("gitworktree: orchestrator refresh skipped for %s: status: %v", path, err)
		return
	}
	if strings.TrimSpace(string(status)) != "" {
		w.logf("gitworktree: orchestrator refresh skipped for %s: worktree has uncommitted changes", path)
		return
	}
	ahead, err := w.run(ctx, w.binary, "-C", path, "rev-list", "--count", target.BaseRef+"..HEAD")
	if err != nil {
		w.logf("gitworktree: orchestrator refresh skipped for %s: compare with %s: %v", path, target.BaseRef, err)
		return
	}
	if n := strings.TrimSpace(string(ahead)); n != "0" {
		w.logf("gitworktree: orchestrator refresh skipped for %s: %s commit(s) not on %s", path, n, target.BaseRef)
		return
	}
	if _, err := w.run(ctx, w.binary, "-C", path, "merge", "--ff-only", "--quiet", target.BaseRef); err != nil {
		w.logf("gitworktree: orchestrator refresh failed for %s: fast-forward to %s: %v", path, target.BaseRef, err)
	}
}
