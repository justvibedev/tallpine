package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// killTeardownBudget bounds each detached teardown phase around cleanup scripts.
// User cleanup commands have no time limit. Sized just
// past the REST layer's default 60s request cap: long enough that a teardown
// which was going to finish still finishes coherently after the caller has
// given up, short enough that a genuinely wedged git or runtime call does not
// hold this session's agent-operation lock (and the connection chi only
// cancels, never aborts) for minutes on end.
const killTeardownBudget = 90 * time.Second

// terminalIntentBudget bounds the two writes that record a kill actually
// happened. They run on their own context because the agent has already
// stopped: refusing the write because the teardown budget ran out leaves a session dead everywhere except
// the row the UI reads, and `tallpine session cleanup` only walks terminated rows, so
// nothing can reach it afterwards (#5463). Short, because these are two small
// local writes, not the git and runtime calls the teardown budget exists for.
const terminalIntentBudget = 15 * time.Second

// recordTermination performs the writes that outlive teardown: the restore
// marker must not survive a user kill (#2319) and the row must end up
// terminated. Deliberately detached from the teardown budget — see
// terminalIntentBudget.
func (m *Manager) recordTermination(ctx context.Context, id domain.SessionID, dropRestoreMarker bool) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), terminalIntentBudget)
	defer cancel()
	var markerErr error
	if dropRestoreMarker {
		markerErr = m.store.DeleteSessionWorktrees(ctx, id)
	}
	if err := m.lcm.MarkTerminated(ctx, id); err != nil {
		return fmt.Errorf("kill %s: %w", id, errors.Join(err, markerErr))
	}
	m.cleanupSystemPromptDir(id)
	if markerErr != nil {
		return fmt.Errorf("kill %s: clear restore marker: %w", id, markerErr)
	}
	return nil
}

func (m *Manager) kill(ctx context.Context, id domain.SessionID, backgroundCleanup, onlyIfLive bool) (KillResult, error) {
	// Teardown deliberately stops riding the caller's context. Kill runs a
	// sequence (stop the agent, tear the controller down, mark the row
	// terminated, drop the worktree) where being cancelled partway leaves a session
	// that is dead in every way except the one the UI reads: the row still says
	// alive while its agent is gone. That is exactly what a caller-side timeout
	// (the REST layer caps a request at cfg.RequestTimeout) or a closed browser
	// tab used to produce. Values still flow through, so request-scoped logging
	// keeps working; only the cancellation is dropped, under its own ceiling so
	// a wedged git or runtime call cannot pin the goroutine forever.
	budget := m.killTeardown
	if budget <= 0 {
		budget = killTeardownBudget
	}
	ctx, cancelTeardown := context.WithTimeout(context.WithoutCancel(ctx), budget)
	defer cancelTeardown()
	if err := m.cancelAsyncChatSpawn(ctx, id); err != nil {
		return KillResult{}, fmt.Errorf("kill %s: cancel provisioning: %w", id, err)
	}

	if err := m.beginAgentOperation(ctx, id, agentOperationKill); err != nil {
		if errors.Is(err, errAgentOperationInProgress) {
			m.logSlotTimeout(id, agentOperationKill, "session busy")
		}
		return KillResult{}, fmt.Errorf("kill %s: %w", id, m.mapOperationInProgress(id, err))
	}
	releaseOperation := func() { m.endAgentOperation(id, agentOperationKill) }
	defer func() {
		if releaseOperation != nil {
			releaseOperation()
		}
	}()

	if active, err := m.hasActiveInterfaceTransition(ctx, id); err != nil {
		return KillResult{}, fmt.Errorf("kill %s: interface transition: %w", id, err)
	} else if active {
		return KillResult{}, fmt.Errorf("kill %s: %w", id, ErrInterfaceTransitionInProgress)
	}
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return KillResult{}, fmt.Errorf("kill %s: %w", id, err)
	}
	if !ok {
		return KillResult{}, nil // already gone: benign race
	}
	if onlyIfLive && rec.IsTerminated {
		return KillResult{AlreadyTerminated: true}, nil
	}
	workspaceProjectRows, workspaceProject, err := m.prepareKillTeardown(ctx, rec)
	if err != nil {
		return KillResult{}, err
	}
	handle := runtimeHandle(rec.Metadata)
	ws := workspaceInfo(rec)
	if err := m.stopSessionController(ctx, rec, handle); err != nil {
		return KillResult{}, err
	}
	// Gate shut any shell terminal scoped to this session BEFORE the worktree
	// goes away: an open shell whose cwd is that directory can otherwise
	// survive the removal (and on Windows can even block it — an open handle
	// on a directory refuses deletion), or a concurrent Open could land a new
	// one in the same race window. A runtime that cannot be confirmed dead
	// stops Kill here — same shape as a dirty-workspace refusal — rather than
	// letting the worktree disappear out from under it.
	var releaseTerminals func()
	defer func() {
		if releaseTerminals != nil {
			releaseTerminals()
		}
	}()
	if ws.Path != "" {
		release, err := m.beginShellTerminalTeardown(ctx, id)
		if err != nil {
			// Same shape as the dirty-workspace refusal below: the worktree is
			// left alone and the session is still terminated.
			return KillResult{}, m.terminateWithPreservedWorkspace(ctx, id, nil, true)
		}
		releaseTerminals = release
	}
	if ws.Path != "" {
		// Persist terminal intent before user commands can wait indefinitely.
		// Workspace-project rows remain inventory for a later cleanup retry.
		if err := m.recordTermination(ctx, id, !workspaceProject); err != nil {
			return KillResult{}, err
		}
		// Another live session may share this worktree path; removing it would
		// pull its cwd away. The session is already terminated; keep the tree.
		if inUse, err := m.isWorkspaceInUse(ctx, rec.ProjectID, ws.Path, id); err != nil {
			m.logger.Warn("kill: workspace ownership check failed; keeping workspace", "sessionID", id, "path", ws.Path, "error", err)
			return KillResult{}, err
		} else if inUse {
			m.logger.Warn("kill: workspace in use by a live session; skipping removal", "sessionID", id, "path", ws.Path)
			if workspaceProject {
				if err := m.recordTermination(ctx, id, true); err != nil {
					return KillResult{}, err
				}
			}
			return KillResult{}, nil
		}
		if backgroundCleanup {
			project, err := m.loadProject(ctx, rec.ProjectID)
			if err != nil {
				return KillResult{}, err
			}
			if project.Kind.WithDefault() != domain.ProjectKindScratch && len(project.Config.PreRemove) > 0 {
				if err := m.recordWorkspaceCleanup(ctx, rec, domain.DispositionPending, ""); err != nil {
					return KillResult{}, err
				}
				// cleanupOne takes its own terminal guard. Keep the agent-operation
				// guard throughout so neither restore nor retry can overlap this job.
				if releaseTerminals != nil {
					releaseTerminals()
					releaseTerminals = nil
				}
				releaseOp := releaseOperation
				releaseOperation = nil
				m.runInBackground(func() {
					defer releaseOp()
					m.backgroundKillCleanup(rec, ws, workspaceProject)
				})
				return KillResult{CleanupPending: true}, nil
			}
		}
		if err := m.runPreRemove(rec.ProjectID, ws.Path); err != nil {
			return KillResult{}, err
		}
		// Give workspace removal its own budget after untimed user commands.
		cancelTeardown()
		var cancelWorkspace context.CancelFunc
		ctx, cancelWorkspace = context.WithTimeout(context.WithoutCancel(ctx), budget)
		defer cancelWorkspace()
	}
	freed, preserved, err := m.destroyKilledWorkspace(ctx, rec, ws, workspaceProjectRows, workspaceProject)
	if preserved {
		// The workspace was kept and the session already terminated; the
		// restore and cleanup inventory must stay for a later retry (#3408).
		return KillResult{}, err
	}
	// Clearing the restore marker keeps the next boot's RestoreAll from
	// resurrecting a killed session (#2319). For workspace projects it must
	// happen after teardown reads the rows; dirty-preserved rows return above
	// and are left as non-restorable inventory.
	if ws.Path == "" || workspaceProject {
		if err := m.recordTermination(ctx, id, true); err != nil {
			return KillResult{}, err
		}
	}
	// Record the outcome so `tallpine session cleanup` and its dry-run skip a
	// workspace this kill already settled.
	if freed {
		m.recordSettledWorkspaceCleanup(ctx, rec, domain.DispositionRemoved)
	} else if ws.Path == "" {
		m.recordSettledWorkspaceCleanup(ctx, rec, domain.DispositionNotApplicable)
	}
	return KillResult{Freed: freed}, nil
}

// RetireForReplacement terminates a live orchestrator and releases its branch
// for a replacement session. Unlike Kill, this captures uncommitted work before
// force-removing the worktree, so a dirty canonical orchestrator worktree does
// not block the replacement from claiming the canonical branch.
//
// This deliberately does not write a session_worktrees row: those rows are
// boot-restore markers, and a replaced orchestrator must stay terminated.
func (m *Manager) RetireForReplacement(ctx context.Context, id domain.SessionID) error {
	if err := m.beginAgentOperation(ctx, id, agentOperationRetire); err != nil {
		return fmt.Errorf("retire replacement %s: %w", id, m.mapOperationInProgress(id, err))
	}
	defer m.endAgentOperation(id, agentOperationRetire)

	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return fmt.Errorf("retire replacement %s: %w", id, err)
	}
	if !ok || rec.IsTerminated {
		return nil
	}
	m.stopPreviewBestEffort(ctx, id)
	m.destroyBrowserBestEffort(ctx, id)
	if rec.Metadata.WorkspacePath == "" || rec.Metadata.Branch == "" {
		if err := m.store.DeleteSessionWorktrees(ctx, rec.ID); err != nil {
			return fmt.Errorf("retire replacement %s: clear restore markers: %w", id, err)
		}
		handle := runtimeHandle(rec.Metadata)
		if err := m.terminateNativeSession(ctx, rec); err != nil {
			return fmt.Errorf("retire replacement %s: native session: %w", id, err)
		}
		if handle.ID != "" {
			if err := m.runtime.Destroy(ctx, handle); err != nil {
				return fmt.Errorf("retire replacement %s: runtime: %w", id, err)
			}
		}
		if err := m.lcm.MarkTerminated(ctx, id); err != nil {
			return fmt.Errorf("retire replacement %s: mark terminated: %w", id, err)
		}
		return nil
	}
	// Gate shut this session's scoped shell terminals before either branch
	// below force-removes its worktree (or worktrees, for a workspace
	// project). Unlike Kill there is no dirty-refusal path here — retirement
	// always force-destroys — so a shell that cannot be confirmed closed fails
	// the whole retirement instead of silently force-removing ground out from
	// under it.
	release, closeErr := m.beginShellTerminalTeardown(ctx, id)
	if closeErr != nil {
		return fmt.Errorf("retire replacement %s: %w", id, closeErr)
	}
	if release != nil {
		defer release()
	}
	if err := m.terminateNativeSession(ctx, rec); err != nil {
		return fmt.Errorf("retire replacement %s: native session: %w", id, err)
	}
	if err := m.importAttachments(ctx, rec); err != nil {
		return fmt.Errorf("retire replacement %s: preserve attachments: %w", id, err)
	}

	if rows, ok, rowErr := m.workspaceProjectRows(ctx, rec); rowErr != nil {
		return fmt.Errorf("retire replacement %s: workspace rows: %w", id, rowErr)
	} else if ok {
		return m.retireWorkspaceProjectForReplacement(ctx, rec, rows)
	}

	ws := workspaceInfo(rec)
	staleWorkspace := false
	if _, err := m.workspace.StashUncommitted(ctx, ws); err != nil {
		if !errors.Is(err, ports.ErrWorkspaceStale) {
			return fmt.Errorf("retire replacement %s: stash: %w", id, err)
		}
		staleWorkspace = true
		m.logger.Warn("retire replacement: stale workspace; skipping preserve", "sessionID", id, "path", ws.Path, "error", err)
	}
	handle := runtimeHandle(rec.Metadata)
	if handle.ID != "" {
		if err := m.runtime.Destroy(ctx, handle); err != nil {
			return fmt.Errorf("retire replacement %s: runtime: %w", id, err)
		}
	}
	if err := m.recordTermination(ctx, rec.ID, true); err != nil {
		return err
	}
	m.runReplacementPreRemove(rec.ID, rec.ProjectID, ws.Path)
	ctx, cancelWorkspace := context.WithTimeout(context.WithoutCancel(ctx), killTeardownBudget)
	defer cancelWorkspace()
	if err := m.workspace.ForceDestroy(ctx, ws); err != nil {
		if staleWorkspace {
			m.logger.Warn("retire replacement: stale workspace cleanup failed", "sessionID", id, "path", ws.Path, "error", err)
		}
		return fmt.Errorf("retire replacement %s: force destroy: %w", id, err)
	}
	m.cleanupAgentWorkspace(ctx, rec, ws.Path)
	return nil
}

func (m *Manager) stopPreviewBestEffort(ctx context.Context, id domain.SessionID) {
	if m.preview == nil {
		return
	}
	if err := m.preview.StopSession(ctx, id); err != nil {
		m.logger.Warn("session preview cleanup failed", "sessionID", id, "error", err)
	}
}

func (m *Manager) terminateNativeSession(ctx context.Context, rec domain.SessionRecord) error {
	if domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeTUI || strings.TrimSpace(rec.Metadata.AgentSessionID) == "" {
		return nil
	}
	if m.agents == nil {
		return fmt.Errorf("%w: %s", ErrUnknownHarness, rec.Harness)
	}
	agent, ok := m.agents.Agent(rec.Harness)
	if !ok {
		return nil
	}
	terminator, ok := agent.(ports.AgentNativeSessionTerminator)
	if !ok {
		return nil
	}
	return terminator.TerminateNativeSession(ctx, ports.SessionRef{
		ID:            string(rec.ID),
		WorkspacePath: rec.Metadata.WorkspacePath,
		DataDir:       m.dataDir,
		Metadata: map[string]string{
			ports.MetadataKeyAgentSessionID: rec.Metadata.AgentSessionID,
		},
	})
}

func (m *Manager) destroyBrowserBestEffort(ctx context.Context, id domain.SessionID) {
	if m.browser == nil {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := m.browser.DestroySession(cleanupCtx, id); err != nil {
		m.logger.Warn("session browser cleanup failed", "sessionID", id, "error", err)
	}
}

func (m *Manager) retireWorkspaceProjectForReplacement(ctx context.Context, rec domain.SessionRecord, rows []ports.WorkspaceRepoInfo) error {
	staleRepos := make(map[string]bool)
	for _, row := range rows {
		if _, err := m.workspace.StashUncommitted(ctx, workspaceInfoFromRepoInfo(row)); err != nil {
			if !errors.Is(err, ports.ErrWorkspaceStale) {
				return fmt.Errorf("retire replacement %s repo %s: stash: %w", rec.ID, row.RepoName, err)
			}
			staleRepos[row.RepoName] = true
			m.logger.Warn("retire replacement: stale workspace repo; skipping preserve", "sessionID", rec.ID, "repo", row.RepoName, "path", row.Path, "error", err)
		}
	}
	handle := runtimeHandle(rec.Metadata)
	if handle.ID != "" {
		if err := m.runtime.Destroy(ctx, handle); err != nil {
			return fmt.Errorf("retire replacement %s: runtime: %w", rec.ID, err)
		}
	}
	if err := m.recordTermination(ctx, rec.ID, false); err != nil {
		return err
	}
	m.runReplacementPreRemove(rec.ID, rec.ProjectID, rec.Metadata.WorkspacePath)
	ctx, cancelWorkspace := context.WithTimeout(context.WithoutCancel(ctx), killTeardownBudget)
	defer cancelWorkspace()
	for i := len(rows) - 1; i >= 0; i-- {
		if err := m.workspace.ForceDestroy(ctx, workspaceInfoFromRepoInfo(rows[i])); err != nil {
			if staleRepos[rows[i].RepoName] {
				m.logger.Warn("retire replacement: stale workspace repo cleanup failed", "sessionID", rec.ID, "repo", rows[i].RepoName, "path", rows[i].Path, "error", err)
			}
			return fmt.Errorf("retire replacement %s repo %s: force destroy: %w", rec.ID, rows[i].RepoName, err)
		}
	}
	m.cleanupAgentWorkspace(ctx, rec, rec.Metadata.WorkspacePath)
	if err := m.store.DeleteSessionWorktrees(ctx, rec.ID); err != nil {
		return fmt.Errorf("retire replacement %s: clear restore markers: %w", rec.ID, err)
	}
	return nil
}

// prepareKillTeardown stops side services and the agent's own state before any
// controller or worktree teardown; a failure leaves the live session intact.
func (m *Manager) prepareKillTeardown(ctx context.Context, rec domain.SessionRecord) ([]ports.WorkspaceRepoInfo, bool, error) {
	id := rec.ID
	m.stopPreviewBestEffort(ctx, id)
	m.destroyBrowserBestEffort(ctx, id)
	var workspaceProjectRows []ports.WorkspaceRepoInfo
	workspaceProject := false
	if rows, ok, rowErr := m.workspaceProjectRows(ctx, rec); rowErr != nil {
		return nil, false, fmt.Errorf("kill %s: workspace rows: %w", id, rowErr)
	} else if ok {
		workspaceProjectRows = rows
		workspaceProject = true
	}
	if err := m.releaseTerminalRuntime(ctx, rec); err != nil {
		return nil, false, fmt.Errorf("kill %s: %w", id, err)
	}
	if err := m.terminateNativeSession(ctx, rec); err != nil {
		return nil, false, fmt.Errorf("kill %s: native session: %w", id, err)
	}
	// Attachments are deliberately ignored by git, so stash cannot preserve
	// legacy worktree-only files. Import them before any controller or worktree
	// teardown; a failed import leaves the live session intact.
	if err := m.importAttachments(ctx, rec); err != nil {
		return nil, false, fmt.Errorf("kill %s: preserve attachments: %w", id, err)
	}
	return workspaceProjectRows, workspaceProject, nil
}

// stopSessionController tears down the one controller a session has, then
// cancels its reviewer.
func (m *Manager) stopSessionController(ctx context.Context, rec domain.SessionRecord, handle ports.RuntimeHandle) error {
	id := rec.ID
	// Exactly one controller exists, so exactly one gets torn down. A chat
	// session has no runtime handle; its controller owns an app-server child
	// process, and closing it also settles any turn left in flight so a later
	// read does not show work that is no longer running.
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
		m.stopChatBestEffort(ctx, id)
	} else if handle.ID != "" {
		// Any Destroy error stops the kill. Every runtime's Destroy already
		// returns nil when it confirms the session is absent, so an error means
		// the runtime may still be live. Re-probing cannot settle it: conpty's
		// IsAlive dials the host's listener, which Destroy's graceful shutdown
		// closes before the PID exits, so a hung host reads as gone. Terminating
		// then would leave a possibly-live agent with no row pointing at it.
		if err := m.runtime.Destroy(ctx, handle); err != nil {
			return fmt.Errorf("kill %s: runtime: %w", id, err)
		}
	}
	if err := m.terminateReviewer(ctx, id, "cancelled by worker session termination"); err != nil {
		return fmt.Errorf("kill %s: reviewer: %w", id, err)
	}
	return nil
}

// destroyKilledWorkspace removes the session's worktree (or workspace-project
// rows). A dirty or failed removal keeps the workspace and terminates the
// session; preserved is then true and err is terminateWithPreservedWorkspace's
// result, so the caller must return it without clearing the restore marker.
func (m *Manager) destroyKilledWorkspace(ctx context.Context, rec domain.SessionRecord, ws ports.WorkspaceInfo, rows []ports.WorkspaceRepoInfo, workspaceProject bool) (freed, preserved bool, err error) {
	id := rec.ID
	if workspaceProject {
		reclaim, err := m.destroyWorkspaceProjectRows(ctx, rows)
		if err != nil {
			return false, true, m.terminateWithPreservedWorkspace(ctx, id, err, false)
		}
		freed = reclaim != ""
		if freed {
			m.cleanupAgentWorkspace(ctx, rec, ws.Path)
		}
	} else if ws.Path != "" {
		if err := m.workspace.Destroy(ctx, ws); err != nil {
			return false, true, m.terminateWithPreservedWorkspace(ctx, id, err, true)
		}
		freed = true
		m.cleanupAgentWorkspace(ctx, rec, ws.Path)
	}
	return freed, false, nil
}

// backgroundKillCleanup runs a killed session's pre-remove cleanup off the
// request path and records the outcome.
func (m *Manager) backgroundKillCleanup(rec domain.SessionRecord, ws ports.WorkspaceInfo, workspaceProject bool) {
	_, reason := m.cleanupOne(m.backgroundContext, rec, ws, nil)
	if reason == "" && workspaceProject {
		if err := m.recordTermination(m.backgroundContext, rec.ID, true); err != nil {
			reason = "workspace teardown failed"
			m.logger.Warn("clear cleanup restore marker", "sessionID", rec.ID, "error", err)
		}
	}
	if err := m.recordWorkspaceCleanupResult(m.backgroundContext, rec, reason); err != nil {
		m.logger.Error("record background workspace cleanup", "sessionID", rec.ID, "error", err)
	}
}
