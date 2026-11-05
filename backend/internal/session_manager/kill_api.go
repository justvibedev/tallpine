package sessionmanager

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// KillResult distinguishes accepted cleanup from a preserved workspace.
type KillResult struct {
	Freed          bool
	CleanupPending bool
	// AlreadyTerminated is set by KillIfLive when it found the row already ended.
	AlreadyTerminated bool
}

// Kill waits for workspace retirement for internal callers that depend on it.
func (m *Manager) Kill(ctx context.Context, id domain.SessionID) (bool, error) {
	result, err := m.kill(ctx, id, false, false)
	return result.Freed, err
}

// RequestKill stops the agent and persists terminal intent before returning.
// Configured cleanup commands use the daemon's existing background workers.
func (m *Manager) RequestKill(ctx context.Context, id domain.SessionID) (KillResult, error) {
	return m.kill(ctx, id, true, false)
}

// KillIfLive is Kill for callers that must not act on a session someone else
// already ended (auto-close): once it wins the operation slot and finds the row
// terminated it does nothing and reports false (otherwise true, even when the workspace is preserved), so the caller does not claim
// the close.
func (m *Manager) KillIfLive(ctx context.Context, id domain.SessionID) (bool, error) {
	result, err := m.kill(ctx, id, false, true)
	return !result.AlreadyTerminated && err == nil, err
}
