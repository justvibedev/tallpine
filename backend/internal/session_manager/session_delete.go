package sessionmanager

import (
	"context"
	"errors"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/domain"
)

var (
	// ErrSessionNotTerminated refuses deleting a session that is still live.
	ErrSessionNotTerminated = errors.New("session: not terminated")
	// ErrWorkspaceNotReleased refuses deleting a session whose workspace may
	// still be on disk: deleting the row would orphan the worktree, since
	// cleanup only reaches workspaces through their session.
	ErrWorkspaceNotReleased = errors.New("session: workspace not released")
	// ErrCleanupRefused is what a CleanupRefusedError wraps.
	ErrCleanupRefused = errors.New("session: workspace cleanup refused")
)

// CleanupRefusedError is a delete whose cleanup-first step left the worktree
// in place, with the reason `tallpine session cleanup` reports.
type CleanupRefusedError struct {
	Reason string
}

func (e *CleanupRefusedError) Error() string { return "workspace cleanup refused: " + e.Reason }

func (e *CleanupRefusedError) Unwrap() error { return ErrCleanupRefused }

// terminatedSessionDeleter is the store's session-record delete. It is
// optional so focused store fakes need not implement it.
type terminatedSessionDeleter interface {
	DeleteTerminatedSession(ctx context.Context, id domain.SessionID) (bool, error)
}

// DeleteSession permanently removes a terminated session's record once its
// workspace is released (or it never had one), along with its Tallpine-owned
// files. With cleanup, a worktree still on disk is first cleaned up the way
// `tallpine session cleanup` does it, never forced. The delete holds the
// session's operation guard, so a restore cannot run between the checks and
// the delete. Branches and PRs are not touched, and the store keeps the
// session's spend in the agent account usage totals and its number reserved.
func (m *Manager) DeleteSession(ctx context.Context, id domain.SessionID, cleanup bool) error {
	deleter, ok := m.store.(terminatedSessionDeleter)
	if !ok {
		return fmt.Errorf("delete %s: session deletion is not supported by this store", id)
	}
	if cleanup {
		if err := m.cleanupBeforeDelete(ctx, id); err != nil {
			return fmt.Errorf("delete %s: %w", id, err)
		}
	}
	if err := m.beginAgentOperation(ctx, id, agentOperationKill); err != nil {
		if errors.Is(err, errAgentOperationInProgress) {
			err = ErrOperationInProgress
		}
		return fmt.Errorf("delete %s: %w", id, err)
	}
	defer m.endAgentOperation(id, agentOperationKill)
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return fmt.Errorf("delete %s: %w", id, err)
	}
	if !ok {
		return fmt.Errorf("delete %s: %w", id, ErrNotFound)
	}
	if !rec.IsTerminated {
		return fmt.Errorf("delete %s: %w", id, ErrSessionNotTerminated)
	}
	rows, err := m.store.ListSessionWorktrees(ctx, id)
	if err != nil {
		return fmt.Errorf("delete %s: %w", id, err)
	}
	if (rec.Metadata.WorkspacePath != "" || len(rows) > 0) && !m.WorkspaceCleanupSettled(ctx, rec) {
		return fmt.Errorf("delete %s: %w", id, ErrWorkspaceNotReleased)
	}
	deleted, err := deleter.DeleteTerminatedSession(ctx, id)
	if err != nil {
		return fmt.Errorf("delete %s: %w", id, err)
	}
	if !deleted {
		return fmt.Errorf("delete %s: %w", id, ErrNotFound)
	}
	m.cleanupSystemPromptDir(id)
	m.cleanupAttachments(ctx, id)
	return nil
}

// cleanupBeforeDelete releases a terminated session's worktree the way
// Cleanup does. Anything else (a live or missing session) is left for the
// delete's own checks to refuse under the operation guard.
func (m *Manager) cleanupBeforeDelete(ctx context.Context, id domain.SessionID) error {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil || !ok || !rec.IsTerminated || m.WorkspaceCleanupSettled(ctx, rec) {
		return err
	}
	var result CleanupResult
	m.cleanupTerminated(ctx, rec, &result, m.cleanupCheck)
	if len(result.Skipped) > 0 {
		return &CleanupRefusedError{Reason: result.Skipped[0].Reason}
	}
	return nil
}
