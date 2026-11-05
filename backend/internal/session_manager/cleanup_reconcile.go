package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// ReconcileWorkspaceCleanupInterval is how often the daemon runs
// ReconcileWorkspaceCleanup.
const ReconcileWorkspaceCleanupInterval = 10 * time.Minute

// cleanupCandidateStore is the scan behind ReconcileWorkspaceCleanup. It is
// optional so focused store fakes need not implement it.
type cleanupCandidateStore interface {
	ListTerminalCleanupCandidates(ctx context.Context, now time.Time) ([]domain.SessionID, error)
}

// ReconcileWorkspaceCleanup is the daemon's periodic pass over terminated
// sessions whose workspace cleanup is unrecorded or unfinished. It finishes
// only what needs no decision from the user:
//
//   - a cleanup Tallpine already accepted (kill or `tallpine session cleanup`
//     recorded it pending) that the daemon stopped before finishing, and
//   - sessions with nothing left on disk to remove, whose stale git
//     registration (if any) it prunes before recording the release.
//
// A worktree still on disk with no accepted cleanup is left for
// `tallpine session cleanup`: this pass never deletes one on its own
// initiative. Sessions holding workspace-project rows are left alone unless
// their cleanup was accepted.
func (m *Manager) ReconcileWorkspaceCleanup(ctx context.Context) error {
	lister, ok := m.store.(cleanupCandidateStore)
	if !ok {
		return nil
	}
	ids, err := lister.ListTerminalCleanupCandidates(ctx, m.now())
	if err != nil {
		return fmt.Errorf("list cleanup candidates: %w", err)
	}
	var errs []error
	for _, id := range ids {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := m.reconcileWorkspaceCleanup(ctx, id); err != nil {
			errs = append(errs, fmt.Errorf("reconcile workspace cleanup %s: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) now() time.Time {
	if m.clock != nil {
		return m.clock()
	}
	return time.Now()
}

func (m *Manager) reconcileWorkspaceCleanup(ctx context.Context, id domain.SessionID) error {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil || !ok || !rec.IsTerminated {
		return err
	}
	facts, found, err := m.store.GetSessionCleanupFacts(ctx, id)
	if err != nil {
		return err
	}
	if found && facts.SessionGeneration == rec.CleanupGeneration && facts.WorkspaceDisposition == domain.DispositionPending {
		return m.resumeAcceptedCleanup(ctx, rec)
	}
	// Sessions this pass leaves alone are decided before taking the operation
	// guard, so revisiting them on every tick never blocks a restore.
	rows, err := m.store.ListSessionWorktrees(ctx, id)
	if err != nil || len(rows) > 0 {
		return err
	}
	if path := rec.Metadata.WorkspacePath; path != "" {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return nil // still on disk (or unreadable): the user decides
		}
	}
	// Nothing left on disk. Cleanup's own path prunes git's stale
	// registration under the workspace gate, skips a path a live successor now
	// holds, and records the release (not_applicable when there was no path).
	var result CleanupResult
	m.cleanupTerminated(ctx, rec, &result, m.cleanupCheck)
	return nil
}

// resumeAcceptedCleanup reruns a pending cleanup the way a user retry does,
// then drops a workspace project's restore marker as a finished background
// kill cleanup would.
func (m *Manager) resumeAcceptedCleanup(ctx context.Context, rec domain.SessionRecord) error {
	_, workspaceProject, err := m.workspaceProjectRows(ctx, rec)
	if err != nil {
		return err
	}
	var result CleanupResult
	m.cleanupTerminated(ctx, rec, &result, m.cleanupCheck)
	if len(result.Skipped) == 0 && workspaceProject {
		return m.recordTermination(ctx, rec.ID, true)
	}
	return nil
}
