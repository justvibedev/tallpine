package sessionmanager

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// recordSettledWorkspaceCleanup records a teardown outcome that needs no
// retry, so later cleanup runs and their dry-run preview skip the session. A
// failed write is only logged: the workspace is already settled, and the next
// cleanup simply revisits the session and finds nothing left.
func (m *Manager) recordSettledWorkspaceCleanup(ctx context.Context, rec domain.SessionRecord, disposition domain.WorkspaceDisposition) {
	if err := m.recordWorkspaceCleanup(ctx, rec, disposition, ""); err != nil {
		m.logger.Warn("record workspace cleanup", "sessionID", rec.ID, "disposition", disposition, "error", err)
	}
}

// WorkspaceCleanupSettled reports a terminated session whose current
// generation already released its workspace, or never had one.
func (m *Manager) WorkspaceCleanupSettled(ctx context.Context, rec domain.SessionRecord) bool {
	if !rec.IsTerminated {
		return false
	}
	facts, ok, err := m.store.GetSessionCleanupFacts(ctx, rec.ID)
	if err != nil {
		m.logger.Warn("read workspace cleanup facts", "sessionID", rec.ID, "error", err)
		return false
	}
	return ok && facts.SessionGeneration == rec.CleanupGeneration && facts.WorkspaceDisposition.Settled()
}
