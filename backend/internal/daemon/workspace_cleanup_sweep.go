package daemon

import (
	"context"
	"log/slog"
	"time"

	sessionmanager "tallpine.eu/tallpine/backend/internal/session_manager"
)

// workspaceCleanupReconciler is the session manager's periodic cleanup pass:
// it finishes interrupted workspace cleanups and records sessions with nothing
// left on disk, and never deletes a worktree the user has not asked to remove.
type workspaceCleanupReconciler interface {
	ReconcileWorkspaceCleanup(ctx context.Context) error
}

// The production session manager runs the pass; focused fakes need not.
var _ workspaceCleanupReconciler = (*sessionmanager.Manager)(nil)

// runWorkspaceCleanupSweep finishes interrupted workspace cleanups every
// ReconcileWorkspaceCleanupInterval. Boot restore settles which terminated
// sessions are coming back before the first pass looks at any of them.
func runWorkspaceCleanupSweep(ctx context.Context, bootDone <-chan struct{}, mgr workspaceCleanupReconciler, log *slog.Logger) {
	select {
	case <-bootDone:
	case <-ctx.Done():
		return
	}
	ticker := time.NewTicker(sessionmanager.ReconcileWorkspaceCleanupInterval)
	defer ticker.Stop()
	for {
		if err := mgr.ReconcileWorkspaceCleanup(ctx); err != nil && ctx.Err() == nil {
			log.Warn("workspace cleanup reconcile failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
