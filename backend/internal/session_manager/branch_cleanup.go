package sessionmanager

import (
	"context"
	"errors"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// branchGatePoll is how often GuardBranchCleanup retries the workspace gate,
// so a cancelled pass stops waiting behind a long restore or spawn.
const branchGatePoll = 50 * time.Millisecond

// GuardBranchCleanup runs fn, which deletes branches of the terminated session
// id, while nothing in its project can restore or spawn: it holds the
// session's agent-operation guard (restore takes the same guard) and the
// project's workspace gate exclusively (restore and spawn take it before they
// attach a worktree). The session is re-read under the guard. A non-empty
// reason, or an error, means fn did not run: the session is live, unknown or
// busy, or another session of the project is being restored and may need a
// branch fn would delete. fn must make its final checks itself, under the
// guard, and must not call back into restore or spawn.
func (m *Manager) GuardBranchCleanup(ctx context.Context, id domain.SessionID, fn func(context.Context)) (string, error) {
	if err := m.beginAgentOperation(ctx, id, agentOperationKill); err != nil {
		if errors.Is(err, errAgentOperationInProgress) {
			return "another operation on the session is running", nil
		}
		return "", err
	}
	defer m.endAgentOperation(id, agentOperationKill)
	rec, ok, err := m.store.GetSession(ctx, id)
	switch {
	case err != nil:
		return "", err
	case !ok:
		return "the session is no longer known", nil
	case !rec.IsTerminated:
		return "the session is live again", nil
	}
	release, err := m.acquireWorkspaceGateContext(ctx, rec.ProjectID)
	if err != nil {
		return "", err
	}
	defer release()
	// A restore takes its session's guard before the gate: one that is
	// waiting for the gate now runs right after fn, so keep its branches.
	if m.restoreInProject(ctx, rec.ProjectID, id) {
		return "a session of the project is being restored", nil
	}
	fn(ctx)
	return "", nil
}

// acquireWorkspaceGateContext acquires the project's workspace gate
// exclusively like acquireWorkspaceGate, but gives up when ctx is done.
func (m *Manager) acquireWorkspaceGateContext(ctx context.Context, projectID domain.ProjectID) (func(), error) {
	mu := m.workspaceGate(projectID)
	for !mu.TryLock() {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(branchGatePoll):
		}
	}
	return mu.Unlock, nil
}

// restoreInProject reports whether a session of project other than self has
// a restore in flight. A session that cannot be read counts.
func (m *Manager) restoreInProject(ctx context.Context, project domain.ProjectID, self domain.SessionID) bool {
	m.agentOpMu.Lock()
	var restoring []domain.SessionID
	for id, kind := range m.agentOperations {
		if kind == agentOperationRestore && id != self {
			restoring = append(restoring, id)
		}
	}
	m.agentOpMu.Unlock()
	for _, id := range restoring {
		rec, ok, err := m.store.GetSession(ctx, id)
		if err != nil || (ok && rec.ProjectID == project) {
			return true
		}
	}
	return false
}
