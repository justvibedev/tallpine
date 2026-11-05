package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// CleanupSkip reports one terminal session whose workspace was preserved
// rather than reclaimed, and why.
type CleanupSkip struct {
	SessionID domain.SessionID
	Reason    string
}

// CleanupResult reports what Cleanup reclaimed and what it preserved.
type CleanupResult struct {
	Cleaned     []domain.SessionID
	AlreadyGone []domain.SessionID
	Skipped     []CleanupSkip
}

// Cleanup reclaims the workspaces of terminal sessions in a project. A workspace
// whose teardown is refused (uncommitted work) is never forced; it is reported
// in Skipped with the reason so the refusal is visible instead of silent.
func (m *Manager) Cleanup(ctx context.Context, project domain.ProjectID) (CleanupResult, error) {
	recs, err := m.cleanupRecords(ctx, project)
	if err != nil {
		return CleanupResult{}, fmt.Errorf("cleanup %s: %w", project, err)
	}
	result := CleanupResult{
		Cleaned:     make([]domain.SessionID, 0, len(recs)),
		AlreadyGone: []domain.SessionID{},
		Skipped:     []CleanupSkip{},
	}
	for _, rec := range recs {
		// A workspace already released (or never created) for the session's
		// current generation is not revisited, so repeat runs report nothing.
		if !rec.IsTerminated || m.WorkspaceCleanupSettled(ctx, rec) {
			continue
		}
		m.cleanupTerminated(ctx, rec, &result, m.cleanupCheck)
	}
	return result, nil
}

// SetCleanupCheck sets the check Cleanup (tallpine session cleanup) and a
// CleanupSession without its own check run under the teardown guard, so a
// manual cleanup refuses the same unsafe worktrees as a scheduled one: what
// the user confirmed in a preview may have changed by the time it runs.
func (m *Manager) SetCleanupCheck(check CleanupCheck) { m.cleanupCheck = check }

// CleanupCheck decides, under the session's teardown guard and right before
// anything is torn down, whether the session's worktrees may go. It gets the
// freshly read record and every worktree path of the session (a workspace
// project's root and children); a non-empty reason keeps the session.
type CleanupCheck func(ctx context.Context, rec domain.SessionRecord, worktrees []string) string

// CleanupSession reclaims one terminated session's workspace exactly as
// Cleanup does for each of a project's sessions. A live or unknown session is
// left alone and reported as skipped.
//
// check, when set, runs under the session's agent-operation guard after the
// session is re-read and before its runtime or workspace is touched, so a
// caller's earlier inspection cannot go stale between its plan and the
// teardown.
func (m *Manager) CleanupSession(ctx context.Context, id domain.SessionID, check CleanupCheck) (CleanupResult, error) {
	result := CleanupResult{Cleaned: []domain.SessionID{}, AlreadyGone: []domain.SessionID{}, Skipped: []CleanupSkip{}}
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return CleanupResult{}, fmt.Errorf("cleanup %s: %w", id, err)
	}
	switch {
	case !ok:
		result.Skipped = append(result.Skipped, CleanupSkip{SessionID: id, Reason: "session not found"})
	case !rec.IsTerminated:
		result.Skipped = append(result.Skipped, CleanupSkip{SessionID: id, Reason: "session is live"})
	default:
		if check == nil {
			check = m.cleanupCheck
		}
		m.cleanupTerminated(ctx, rec, &result, check)
	}
	return result, nil
}

// SessionWorktreePaths lists the session's worktree paths: its workspace path
// and every per-repo worktree row (a workspace project's root and children),
// without duplicates.
func (m *Manager) SessionWorktreePaths(ctx context.Context, id domain.SessionID) ([]string, error) {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	return m.sessionWorktreePaths(ctx, rec)
}

func (m *Manager) sessionWorktreePaths(ctx context.Context, rec domain.SessionRecord) ([]string, error) {
	rows, err := m.store.ListSessionWorktrees(ctx, rec.ID)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var paths []string
	add := func(path string) {
		if path = strings.TrimSpace(path); path == "" {
			return
		}
		if key := normalizeWorkspacePath(path); !seen[key] {
			seen[key] = true
			paths = append(paths, path)
		}
	}
	add(rec.Metadata.WorkspacePath)
	for _, row := range rows {
		add(row.WorktreePath)
	}
	return paths, nil
}

func (m *Manager) cleanupTerminated(ctx context.Context, rec domain.SessionRecord, result *CleanupResult, check CleanupCheck) {
	// A preceding session's cleanup commands can outlive the request deadline.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), killTeardownBudget)
	defer cancel()
	// Take the session's agent-operation guard before touching anything, and
	// re-read the row under it. Restore holds the same guard, so a session the
	// caller listed as terminated but that has been restored since is seen as
	// live here, and neither its runtime nor its workspace is torn down.
	if err := m.beginAgentOperation(ctx, rec.ID, agentOperationKill); err != nil {
		reason := "workspace teardown failed"
		if errors.Is(err, errAgentOperationInProgress) {
			reason = "workspace cleanup is already running"
		}
		result.Skipped = append(result.Skipped, CleanupSkip{SessionID: rec.ID, Reason: reason})
		return
	}
	defer m.endAgentOperation(rec.ID, agentOperationKill)
	cur, ok, err := m.store.GetSession(ctx, rec.ID)
	switch {
	case err != nil:
		m.logger.Error("cleanup: re-read session", "sessionID", rec.ID, "error", err)
		result.Skipped = append(result.Skipped, CleanupSkip{SessionID: rec.ID, Reason: "workspace teardown failed"})
		return
	case !ok:
		return
	case !cur.IsTerminated || cur.CleanupGeneration != rec.CleanupGeneration:
		result.Skipped = append(result.Skipped, CleanupSkip{SessionID: rec.ID, Reason: "session was restored"})
		return
	}
	rec = cur
	if check != nil {
		paths, err := m.sessionWorktreePaths(ctx, rec)
		if err != nil {
			m.logger.Error("cleanup: list session worktrees", "sessionID", rec.ID, "error", err)
			result.Skipped = append(result.Skipped, CleanupSkip{SessionID: rec.ID, Reason: "worktree status could not be read"})
			return
		}
		if reason := check(ctx, rec, paths); reason != "" {
			result.Skipped = append(result.Skipped, CleanupSkip{SessionID: rec.ID, Reason: reason})
			return
		}
	}
	ws := workspaceInfo(rec)
	if ws.Path == "" {
		m.cleanupAgentWorkspace(ctx, rec, "")
		m.cleanupSystemPromptDir(rec.ID)
		m.recordSettledWorkspaceCleanup(ctx, rec, domain.DispositionNotApplicable)
		return
	}
	// Runtime teardown is keyed on the terminated session's own handle, not
	// the workspace path, so it runs even when the workspace is shared with a
	// live successor — otherwise a skipped session would leak its runtime
	// (the lingering keep-alive shell) until cleanup reruns.
	// Deliberately run before acquiring the workspace gate so that any code
	// path Destroy invokes synchronously (for example, a test fake or a future
	// runtime adapter that spawns a successor during teardown) can itself call
	// Spawn or Restore without deadlocking on the gate.
	if h := runtimeHandle(rec.Metadata); h.ID != "" {
		_ = m.runtime.Destroy(ctx, h) // best effort; usually already gone
	}
	reclaim, reason := m.cleanupWorkspaceUnderGate(ctx, rec, ws, check)
	if reason != "" {
		result.Skipped = append(result.Skipped, CleanupSkip{SessionID: rec.ID, Reason: reason})
		return
	}
	m.cleanupSystemPromptDir(rec.ID)
	if reclaim == ports.WorkspaceReclaimAlreadyAbsent {
		result.AlreadyGone = append(result.AlreadyGone, rec.ID)
		return
	}
	result.Cleaned = append(result.Cleaned, rec.ID)
}

// cleanupWorkspaceUnderGate records and performs the workspace teardown of a
// terminated session. The caller holds the session's agent-operation guard.
// Spawn and Restore hold the per-project workspace gate while they allocate a
// workspace and commit metadata, so isWorkspaceInUse cannot race with an
// in-progress spawn that has not yet written WorkspacePath to the store.
// User commands run outside the gate; cleanupOne rechecks ownership under it
// before removal.
// Returns an empty reason when the workspace was reclaimed; a non-empty
// reason means it was left alone this run and the reclaim value is undefined.
func (m *Manager) cleanupWorkspaceUnderGate(ctx context.Context, rec domain.SessionRecord, ws ports.WorkspaceInfo, check CleanupCheck) (ports.WorkspaceReclaim, string) {
	facts, tracked, err := m.store.GetSessionCleanupFacts(ctx, rec.ID)
	if err != nil {
		m.logger.Error("read workspace cleanup facts", "sessionID", rec.ID, "error", err)
		return ports.WorkspaceReclaimRemoved, "workspace teardown failed"
	}
	tracked = tracked && facts.SessionGeneration == rec.CleanupGeneration
	if tracked {
		if err := m.recordWorkspaceCleanup(ctx, rec, domain.DispositionPending, ""); err != nil {
			return ports.WorkspaceReclaimRemoved, "workspace teardown failed"
		}
	}
	reclaim, reason := m.cleanupOne(ctx, rec, ws, check)
	switch {
	case tracked:
		if err := m.recordWorkspaceCleanupResult(ctx, rec, reason); err != nil {
			m.logger.Error("record retried workspace cleanup", "sessionID", rec.ID, "error", err)
			return reclaim, "workspace teardown failed"
		}
	case reason == "":
		// A sweep-found session gets only a released record, never a failure,
		// so an old preserved worktree does not start showing a cleanup error
		// in the app; later runs skip the released one.
		m.recordSettledWorkspaceCleanup(ctx, rec, domain.DispositionRemoved)
	}
	return reclaim, reason
}

// isWorkspaceInUse reports whether any non-terminated session in the project
// references the given workspace path. Must be called under the project's
// workspace gate; see cleanupWorkspaceUnderGate for the full invariant.
func (m *Manager) isWorkspaceInUse(ctx context.Context, projectID domain.ProjectID, workspacePath string, except domain.SessionID) (bool, error) {
	if workspacePath == "" {
		return false, nil
	}
	recs, err := m.cleanupRecords(ctx, projectID)
	if err != nil {
		return false, err
	}
	for _, rec := range recs {
		if rec.ID != except && !rec.IsTerminated && rec.Metadata.WorkspacePath != "" &&
			normalizeWorkspacePath(rec.Metadata.WorkspacePath) == normalizeWorkspacePath(workspacePath) {
			return true, nil
		}
	}
	return false, nil
}

// cleanupOne reclaims one terminated session's workspace, gating shut any
// shell terminal scoped to it first (same ordering as Kill). Split out of
// Cleanup's loop so the release function's defer is scoped to one session's
// call, not deferred across every iteration until Cleanup itself returns.
// Returns "" when the workspace was reclaimed; a non-empty reason means it was
// left alone this run (Cleanup records it in Skipped and can retry on a later
// call) — most commonly because a scoped shell terminal could not be
// confirmed closed, so reclaiming would pull the ground out from under it.
func (m *Manager) cleanupOne(ctx context.Context, rec domain.SessionRecord, ws ports.WorkspaceInfo, check CleanupCheck) (ports.WorkspaceReclaim, string) {
	{
		release := m.acquireWorkspaceGate(rec.ProjectID)

		inUse, err := m.isWorkspaceInUse(ctx, rec.ProjectID, ws.Path, "")
		release()
		if err != nil {
			m.logger.Warn("cleanup: workspace ownership check failed", "sessionID", rec.ID, "projectID", rec.ProjectID, "error", err)
			return ports.WorkspaceReclaimRemoved, "workspace teardown failed"
		}
		if inUse {
			return ports.WorkspaceReclaimRemoved, "workspace in use by a live session"
		}
	}
	release, closeErr := m.beginShellTerminalTeardown(ctx, rec.ID)
	if closeErr != nil {
		m.logger.Warn("cleanup: shell terminal still open", "sessionID", rec.ID, "error", closeErr)
		return ports.WorkspaceReclaimRemoved, "shell terminal still open"
	}
	if release != nil {
		defer release()
	}
	if err := m.importAttachments(ctx, rec); err != nil {
		m.logger.Warn("cleanup: attachment preservation failed", "sessionID", rec.ID, "error", err)
		return ports.WorkspaceReclaimRemoved, "attachment preservation failed"
	}
	if err := m.runPreRemove(rec.ProjectID, ws.Path); err != nil {
		m.logger.Warn("cleanup: workspace script failed; preserving workspace", "sessionID", rec.ID, "error", err)
		return ports.WorkspaceReclaimRemoved, cleanupSkipReason(err)
	}
	ctx, cancelWorkspace := context.WithTimeout(m.backgroundContext, killTeardownBudget)
	defer cancelWorkspace()
	// The project's pre-remove script ran in the worktree and may have changed
	// it (left files, started a rebase): the check runs again here. Its earlier
	// run only spared scripts for worktrees that were going to be kept anyway.
	// It runs before the workspace gate, which spawns and restores of the
	// whole project wait on: the session's agent-operation guard already keeps
	// this session from being restored, and the gate only guards the
	// ownership decision below.
	if check != nil {
		paths, err := m.sessionWorktreePaths(ctx, rec)
		if err != nil {
			m.logger.Warn("cleanup: list session worktrees", "sessionID", rec.ID, "error", err)
			return ports.WorkspaceReclaimRemoved, "worktree status could not be read"
		}
		if reason := check(ctx, rec, paths); reason != "" {
			return ports.WorkspaceReclaimRemoved, reason
		}
	}
	releaseWorkspaceGate := m.acquireWorkspaceGate(rec.ProjectID)
	defer releaseWorkspaceGate()
	if inUse, err := m.isWorkspaceInUse(ctx, rec.ProjectID, ws.Path, ""); err != nil {
		m.logger.Warn("cleanup: workspace ownership check failed", "sessionID", rec.ID, "error", err)
		return ports.WorkspaceReclaimRemoved, "workspace teardown failed"
	} else if inUse {
		return ports.WorkspaceReclaimRemoved, "workspace in use by a live session"
	}

	if rows, ok, rowErr := m.workspaceProjectRows(ctx, rec); rowErr != nil {
		m.logger.Warn("cleanup: workspace rows failed", "sessionID", rec.ID, "error", rowErr)
		return ports.WorkspaceReclaimRemoved, "workspace teardown failed"
	} else if ok {
		reclaim, err := m.destroyWorkspaceProjectRows(ctx, rows)
		if err != nil {
			if !expectedWorkspaceRefusal(err) {
				m.logger.Warn("cleanup: workspace teardown failed", "sessionID", rec.ID, "path", ws.Path, "error", err)
			}
			return ports.WorkspaceReclaimRemoved, cleanupSkipReason(err)
		}
		m.cleanupAgentWorkspace(ctx, rec, ws.Path)
		return reclaim, ""
	}
	reclaim := ports.WorkspaceReclaimRemoved
	var err error
	if reclaimer, ok := m.workspace.(ports.WorkspaceReclaimer); ok {
		reclaim, err = reclaimer.DestroyReclaim(ctx, ws)
	} else {
		err = m.workspace.Destroy(ctx, ws)
	}
	if err != nil {
		if !expectedWorkspaceRefusal(err) {
			// The public reason stays a fixed string (the raw error carries
			// internal filesystem paths); the full cause lands here.
			m.logger.Warn("cleanup: workspace teardown failed", "sessionID", rec.ID, "path", ws.Path, "error", err)
		}
		return ports.WorkspaceReclaimRemoved, cleanupSkipReason(err)
	}
	m.cleanupAgentWorkspace(ctx, rec, ws.Path)
	return reclaim, ""
}

// cleanupSkipReason renders a workspace teardown refusal as a short
// user-facing reason for the cleanup report. Deliberately not the raw error:
// it flows to the API response and CLI output, and teardown errors embed
// internal filesystem paths.
func cleanupSkipReason(err error) string {
	var step *cleanupStepError
	if errors.As(err, &step) {
		return step.Error()
	}
	if errors.Is(err, ErrCleanupScript) {
		return "workspace cleanup script failed"
	}
	if errors.Is(err, ports.ErrWorkspaceDirty) {
		return "workspace has uncommitted changes"
	}
	if errors.Is(err, ports.ErrWorkspaceLocked) {
		return "worktree is locked; run git worktree unlock to clean it up"
	}
	if errors.Is(err, ports.ErrWorkspaceDeferred) {
		return "worktree is in use; will retry on a later run"
	}
	if errors.Is(err, ports.ErrWorkspaceRepoUnavailable) {
		return "project repository is missing; remove worktree manually"
	}
	if errors.Is(err, ErrProjectNotResolvable) {
		return "project is archived or unregistered — remove worktree manually"
	}
	return "workspace teardown failed"
}

func (m *Manager) cleanupRecords(ctx context.Context, project domain.ProjectID) ([]domain.SessionRecord, error) {
	if project == "" {
		return m.store.ListAllSessions(ctx)
	}
	return m.store.ListSessions(ctx, project)
}

// normalizeWorkspacePath canonicalizes a workspace path for set membership so
// two records naming the same directory (a terminated predecessor and its live
// successor) compare equal despite trailing slashes or "." segments.
func normalizeWorkspacePath(p string) string {
	return filepath.Clean(p)
}

// ---- helpers ----
