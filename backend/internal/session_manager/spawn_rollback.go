package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func (m *Manager) destroySpawnWorkspace(ctx context.Context, ws ports.WorkspaceInfo, workspaceProject *ports.WorkspaceProjectInfo) bool {
	if release, ok := ctx.Value(spawnWorkspaceGateKey{}).(func()); ok {
		release()
	}
	release := m.acquireWorkspaceGate(ws.ProjectID)
	inUse, err := m.isWorkspaceInUse(ctx, ws.ProjectID, ws.Path, ws.SessionID)
	release()
	if err != nil || inUse {
		m.logger.Warn("spawn rollback: workspace ownership changed; preserving workspace", "sessionID", ws.SessionID, "error", err)
		return false
	}
	if err := m.runPreRemove(ws.ProjectID, ws.Path); err != nil {
		m.logger.Warn("spawn rollback: workspace cleanup failed; preserving workspace", "sessionID", ws.SessionID, "error", err)
		return false
	}
	ctx, cancel := spawnRollbackContext(ctx)
	defer cancel()
	release = m.acquireWorkspaceGate(ws.ProjectID)
	defer release()
	if inUse, err := m.isWorkspaceInUse(ctx, ws.ProjectID, ws.Path, ws.SessionID); err != nil || inUse {
		m.logger.Warn("spawn rollback: workspace ownership changed; preserving workspace", "sessionID", ws.SessionID, "error", err)
		return false
	}
	if workspaceProject != nil {
		if adapter, ok := m.workspace.(ports.WorkspaceProject); ok {
			err = adapter.DestroyWorkspaceProject(ctx, *workspaceProject)
		} else {
			err = m.workspace.Destroy(ctx, ws)
		}
	} else {
		err = m.workspace.Destroy(ctx, ws)
	}
	if err != nil {
		return false
	}
	_ = m.store.DeleteSessionWorktrees(ctx, ws.SessionID)
	return true
}

var spawnRollbackBudget = 30 * time.Second

func spawnRollbackContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(ctx), spawnRollbackBudget)
}

// destroyPossibleRuntimeAfterCreateFailure tears down the runtime a failed
// Create reported it may have left behind (ports.RuntimeEffectError), unless the
// runtime already cleaned it up. Destroying a runtime that never started is a
// no-op, so a timeout whose effect is unknown is cleaned up rather than assumed
// harmless. It reports ok=false, with the handle that may still be running,
// when the destroy failed.
func (m *Manager) destroyPossibleRuntimeAfterCreateFailure(ctx context.Context, createErr error) (ports.RuntimeHandle, bool) {
	var effect ports.RuntimeEffectError
	if !errors.As(createErr, &effect) || effect.EffectOutcome() == ports.RuntimeEffectNone {
		return ports.RuntimeHandle{}, true
	}
	if effect.CleanupOutcome() == ports.RuntimeCleanupSucceeded {
		return ports.RuntimeHandle{}, true
	}
	handle := effect.PossibleHandle()
	if handle.ID == "" {
		return ports.RuntimeHandle{}, true
	}
	if !m.destroySpawnRuntimeAfterFailure(ctx, handle) {
		m.logger.Warn("spawn rollback: could not destroy runtime left by a failed create; keeping session for reaping", "handle", handle.ID, "error", createErr)
		return handle, false
	}
	return handle, true
}

// keepSpawnRecordForLeakedRuntime preserves the workspace of a spawn whose
// runtime could not be destroyed, records the runtime handle on the session, and
// parks the session terminated. The startup reconcile reaps terminated sessions'
// recorded runtimes, so the leaked runtime is not orphaned without a record.
func (m *Manager) keepSpawnRecordForLeakedRuntime(ctx context.Context, rec domain.SessionRecord, ws ports.WorkspaceInfo, handle ports.RuntimeHandle) {
	cleanupCtx, cancel := spawnRollbackContext(ctx)
	defer cancel()
	cur, ok, err := m.store.GetSession(cleanupCtx, rec.ID)
	if err != nil || !ok {
		m.logger.Warn("spawn rollback: cannot record leaked runtime", "sessionID", rec.ID, "handle", handle.ID, "error", err)
		return
	}
	cur.Metadata.Branch = ws.Branch
	cur.Metadata.WorkspacePath = ws.Path
	cur.Metadata.WorkspaceRepoPath = ws.RepoPath
	cur.Metadata.RuntimeHandleID = handle.ID
	if err := m.store.UpdateSession(cleanupCtx, cur); err != nil {
		m.logger.Warn("spawn rollback: failed to record leaked runtime", "sessionID", rec.ID, "handle", handle.ID, "error", err)
	}
	m.markSpawnFailedTerminated(cleanupCtx, rec.ID)
}

func (m *Manager) destroySpawnRuntimeAfterFailure(ctx context.Context, handle ports.RuntimeHandle) bool {
	cleanupCtx, cancel := spawnRollbackContext(ctx)
	defer cancel()
	return m.runtime.Destroy(cleanupCtx, handle) == nil
}

func (m *Manager) rollbackSpawnSeedRowAfterFailure(ctx context.Context, id domain.SessionID) {
	cleanupCtx, cancel := spawnRollbackContext(ctx)
	defer cancel()
	m.rollbackSpawnSeedRow(cleanupCtx, id)
}

func (m *Manager) rollbackPreparedSpawnWorkspaceAfterFailure(ctx context.Context, rec domain.SessionRecord, ws ports.WorkspaceInfo, workspaceProject *ports.WorkspaceProjectInfo, runtimeDestroyed bool) bool {
	cleanupCtx, cancel := spawnRollbackContext(ctx)
	workspaceDestroyed := m.destroySpawnWorkspace(cleanupCtx, ws, workspaceProject)
	cancel()
	if workspaceDestroyed {
		cleanupCtx, cancel = spawnRollbackContext(ctx)
		m.cleanupAgentWorkspace(cleanupCtx, rec, ws.Path)
		cancel()
		return true
	}
	cleanupCtx, cancel = spawnRollbackContext(ctx)
	m.preserveFailedSpawnWorkspace(cleanupCtx, rec.ID, ws, runtimeDestroyed)
	cancel()
	return false
}

func (m *Manager) markSpawnFailedTerminatedAfterFailure(ctx context.Context, id domain.SessionID, withoutWorkspace bool) {
	cleanupCtx, cancel := spawnRollbackContext(ctx)
	defer cancel()
	if withoutWorkspace {
		m.markSpawnFailedTerminatedWithoutWorkspace(cleanupCtx, id)
		return
	}
	m.markSpawnFailedTerminated(cleanupCtx, id)
}

func (m *Manager) rollbackSeedSpawnWorkspace(ctx context.Context, rec domain.SessionRecord, ws ports.WorkspaceInfo, workspaceProject *ports.WorkspaceProjectInfo, prepared, published bool) {
	cleanupCtx, cancel := spawnRollbackContext(ctx)
	workspaceDestroyed := m.destroySpawnWorkspace(cleanupCtx, ws, workspaceProject)
	cancel()
	if workspaceDestroyed {
		if prepared {
			cleanupCtx, cancel = spawnRollbackContext(ctx)
			m.cleanupAgentWorkspace(cleanupCtx, rec, ws.Path)
			cancel()
		}
		if published {
			m.clearProvisionedWorkspace(ctx, rec.ID, ws.Path)
		} else {
			m.rollbackSpawnSeedRowAfterFailure(ctx, rec.ID)
			m.cleanupArtifactDir(rec.ID)
		}
		return
	}
	cleanupCtx, cancel = spawnRollbackContext(ctx)
	m.preserveFailedSpawnWorkspace(cleanupCtx, rec.ID, ws, true)
	cancel()
	if published {
		return
	}
	m.markSpawnFailedTerminatedAfterFailure(ctx, rec.ID, false)
}

func (m *Manager) preserveFailedSpawnWorkspace(ctx context.Context, id domain.SessionID, ws ports.WorkspaceInfo, runtimeDestroyed bool) {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		m.logger.Warn("spawn rollback: failed to load session for preserved workspace", "sessionID", id, "workspacePath", ws.Path, "error", err)
		return
	}
	if !ok {
		m.logger.Warn("spawn rollback: session missing for preserved workspace", "sessionID", id, "workspacePath", ws.Path)
		return
	}
	rec.Metadata.Branch = ws.Branch
	rec.Metadata.WorkspacePath = ws.Path
	rec.Metadata.WorkspaceRepoPath = ws.RepoPath
	if runtimeDestroyed {
		rec.Metadata.RuntimeHandleID = ""
		rec.Metadata.RuntimeLaunchID = ""
	}
	if err := m.store.UpdateSession(ctx, rec); err != nil {
		m.logger.Warn("spawn rollback: failed to record preserved workspace", "sessionID", id, "workspacePath", ws.Path, "error", err)
	}
}

// markSpawnFailedTerminated best-effort parks an orphaned spawn as terminated.
// A phantom half-spawned row is worse than a terminal one; we only delete the
// row when nothing observable has landed yet (seed state) via rollbackSpawn or
// rollbackSpawnSeedRow.
func (m *Manager) markSpawnFailedTerminated(ctx context.Context, id domain.SessionID) {
	_ = m.lcm.MarkTerminated(ctx, id)
	m.cleanupSystemPromptDir(id)
}

// markSpawnFailedTerminatedWithoutWorkspace parks a spawn failure after the
// runtime row had become observable, but clears launch handles for resources
// that were destroyed during rollback. This keeps later restore/cleanup paths
// from treating a removed worktree as reusable state.
func (m *Manager) markSpawnFailedTerminatedWithoutWorkspace(ctx context.Context, id domain.SessionID) {
	m.markSpawnFailedTerminated(ctx, id)
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil || !ok {
		return
	}
	rec.Metadata.Branch = ""
	rec.Metadata.WorkspacePath = ""
	rec.Metadata.RuntimeHandleID = ""
	rec.Metadata.AgentSessionID = ""
	_ = m.store.UpdateSession(ctx, rec)
}

// rollbackSpawnSeedRow best-effort removes the row of a spawn that failed
// before anything observable (worktree, runtime) was built, so failed spawns
// don't accumulate terminated rows in session lists. DeleteSession only removes
// rows still in seed state; if the row has progressed or the delete itself
// fails, fall back to parking it terminated so a phantom row never looks live.
func (m *Manager) rollbackSpawnSeedRow(ctx context.Context, id domain.SessionID) {
	if deleted, err := m.store.DeleteSession(ctx, id); err == nil && deleted {
		m.cleanupSystemPromptDir(id)
		m.cleanupAttachments(ctx, id)
		return
	}
	m.markSpawnFailedTerminated(ctx, id)
}

// rollbackSpawn deletes a session row when it is still in seed state — used
// when an out-of-band step that happens AFTER `Spawn` returns (e.g. PR claim
// over HTTP) has failed and the caller wants the partially-spawned session
// gone without leaving a terminated orphan visible under `--include-terminated`.
//
// If the row has progressed past seed state (workspace exists, runtime created,
// etc.), DeleteSession is a no-op and rollbackSpawn falls back to a Kill so the
// runtime/workspace are torn down. Returns (deleted, killed):
//   - deleted=true: the row was a seed row and has been removed
//   - killed=true:  the row had spawn output and was torn down + terminated
//   - both false:   the row was already terminated or absent — benign no-op
func (m *Manager) rollbackSpawn(ctx context.Context, id domain.SessionID) (deleted, killed bool, err error) {
	deleted, err = m.store.DeleteSession(ctx, id)
	if err != nil {
		return false, false, fmt.Errorf("rollback %s: %w", id, err)
	}
	if deleted {
		m.cleanupSystemPromptDir(id)
		m.cleanupAttachments(ctx, id)
		return true, false, nil
	}
	killed, err = m.Kill(ctx, id)
	if err != nil {
		return false, false, err
	}
	return false, killed, nil
}

// RollbackSpawn is the public surface of rollbackSpawn for service-layer callers.
func (m *Manager) RollbackSpawn(ctx context.Context, id domain.SessionID) (deleted, killed bool, err error) {
	return m.rollbackSpawn(ctx, id)
}

// expectedWorkspaceRefusal reports a teardown refusal Tallpine already has a name
// for: uncommitted work that is deliberately never force-removed, a project
// whose repository has been deleted, or a directory still pinned by a process
// handle (Windows sharing violation). It only decides log volume — every
// workspace teardown failure preserves the worktree, named or not.
func expectedWorkspaceRefusal(err error) bool {
	return errors.Is(err, ports.ErrWorkspaceDirty) ||
		errors.Is(err, ports.ErrWorkspaceRepoUnavailable) ||
		errors.Is(err, ports.ErrWorkspaceDeferred) ||
		errors.Is(err, ErrProjectNotResolvable)
}

// terminateWithPreservedWorkspace records terminal intent for a session whose
// workspace could not be released. Nothing was force-removed, so the worktree
// is still on disk for `tallpine session cleanup` to retry and report on; what must
// not survive is the session's claim on the sidebar. dropRestoreMarker is false
// only for workspace projects, whose rows are left as non-restorable inventory
// for the same retry.
func (m *Manager) terminateWithPreservedWorkspace(ctx context.Context, id domain.SessionID, cause error, dropRestoreMarker bool) error {
	if cause != nil && !expectedWorkspaceRefusal(cause) {
		m.logger.Warn("kill: workspace teardown failed; worktree preserved", "sessionID", id, "error", cause)
	}
	return m.recordTermination(ctx, id, dropRestoreMarker)
}
