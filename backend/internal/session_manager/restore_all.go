package sessionmanager

import (
	"context"
	"errors"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// RestoreAll relaunches every terminated session that was saved by the last
// SaveAndTeardownAll. The "shutdown-saved" marker is the presence of a
// session_worktrees row for the session; sessions the user killed before
// shutdown have no such row and are left terminated.
//
// For each saved session:
//  1. Ensure the worktree exists via workspace.Restore.
//  2. If a preserve ref is recorded, replay it via ApplyPreserved; on conflict
//     log and continue (still relaunch the agent, never delete the ref).
//  3. Relaunch via the existing Restore method.
//
// Failures on individual sessions are logged and do not abort the loop.
func (m *Manager) RestoreAll(ctx context.Context) error {
	recs, err := m.store.ListAllSessions(ctx)
	if err != nil {
		return fmt.Errorf("restore-all: list sessions: %w", err)
	}
	for _, rec := range recs {
		if !rec.IsTerminated {
			continue
		}
		if rec.Harness.IsUnsupported() {
			m.logger.Warn("restore-all: skipping session with an unsupported harness", "sessionID", rec.ID, "harness", rec.Harness)
			continue
		}
		m.restoreAllSession(ctx, rec)
	}
	return nil
}

// restoreAllSession restores one terminated session marked for restore at
// shutdown, acquiring the project workspace gate so cleanup does not tear down
// its workspace while restore is in progress.
func (m *Manager) restoreAllSession(ctx context.Context, rec domain.SessionRecord) {
	releaseWorkspaceGate := m.acquireWorkspaceGate(rec.ProjectID)
	defer releaseWorkspaceGate()

	// Check the shutdown-saved marker: is there a session_worktrees row?
	rows, err := m.store.ListSessionWorktrees(ctx, rec.ID)
	if err != nil {
		m.logger.Error("restore-all: list worktrees failed", "sessionID", rec.ID, "error", err)
		return
	}
	if len(rows) == 0 {
		// No marker: this session was killed by the user before shutdown.
		return
	}
	rows = restorableWorktreeRows(rows)
	if len(rows) == 0 {
		return
	}
	recreated := false
	for _, row := range rows {
		path := row.WorktreePath
		if path == "" {
			path = rec.Metadata.WorkspacePath
		}
		missing, statErr := workspacePathMissing(path)
		if statErr != nil {
			m.logger.Error("restore-all: inspect workspace failed", "sessionID", rec.ID, "error", statErr)
			return
		}
		recreated = recreated || missing
	}

	// Step 1: ensure the worktree exists. workspace.Restore re-creates it
	// if it was removed by SaveAndTeardownAll.
	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		m.logger.Error("restore-all: load project failed", "sessionID", rec.ID, "error", err)
		return
	}
	var ws ports.WorkspaceInfo
	restoredWorkspaceProject := project.Kind.WithDefault() == domain.ProjectKindWorkspace
	var projectRows []ports.WorkspaceRepoInfo
	if restoredWorkspaceProject {
		var rowErr error
		projectRows, rowErr = m.workspaceProjectRestoreRowsFromMarkers(ctx, project, rec, rows)
		if rowErr != nil {
			m.logger.Error("restore-all: workspace rows failed", "sessionID", rec.ID, "error", rowErr)
			return
		}
		root, restoreErr := m.restoreWorkspaceProjectRows(ctx, projectRows)
		if restoreErr != nil {
			m.logger.Error("restore-all: workspace project restore failed", "sessionID", rec.ID, "error", restoreErr)
			return
		}
		ws = workspaceInfoFromRepoInfo(root)
	} else {
		var restoreErr error
		ws, restoreErr = m.workspace.Restore(ctx, ports.WorkspaceConfig{
			ProjectID:     rec.ProjectID,
			SessionID:     rec.ID,
			Kind:          rec.Kind,
			SessionPrefix: sessionPrefix(project),
			Branch:        rec.Metadata.Branch,
			BaseBranch:    project.Config.WorktreeBaseBranch(),
			BaseRef:       rec.Metadata.DiffBaseRef,
			Path:          rec.Metadata.WorkspacePath,

			// Only sessions terminated at shutdown get here: no agent is attached.
			RefreshOrchestrator: true,
		})
		if restoreErr != nil {
			m.logger.Error("restore-all: workspace restore failed", "sessionID", rec.ID, "error", restoreErr)
			return
		}
	}
	if ws.Path == "" {
		m.logger.Error("restore-all: workspace restore failed", "sessionID", rec.ID, "error", "empty restored root path")
		return
	}
	if err := m.restoreAttachments(ctx, rec.ID, ws); err != nil {
		m.logger.Error("restore-all: restore attachments failed", "sessionID", rec.ID, "error", err)
		return
	}

	// Step 2: replay preserve ref when one was recorded.
	if restoredWorkspaceProject {
		m.applyWorkspaceProjectPreserved(ctx, projectRows)
	} else {
		var preserveRef string
		for _, r := range rows {
			if r.PreservedRef != "" {
				preserveRef = r.PreservedRef
				break
			}
		}
		if preserveRef != "" {
			if applyErr := m.workspace.ApplyPreserved(ctx, ws, preserveRef); applyErr != nil {
				if errors.Is(applyErr, ports.ErrPreservedConflict) {
					m.logger.Warn("restore-all: apply preserved produced conflicts; agent relaunched with conflict markers in place",
						"sessionID", rec.ID, "ref", preserveRef, "error", applyErr)
				} else {
					m.logger.Error("restore-all: apply preserved failed", "sessionID", rec.ID, "error", applyErr)
				}
				// Continue: always relaunch even on conflict (never delete the ref here).
			}
		}
	}
	if recreated {
		if err := m.provisionWorkspace(ctx, project, ws.Path); err != nil {
			m.logger.Error("restore-all: setup recreated workspace failed", "sessionID", rec.ID, "error", err)
			return
		}
	}

	// Step 3: relaunch the agent in the restored workspace.
	if _, err := m.relaunchRestoredSession(ctx, rec, project, ws); err != nil {
		switch {
		case errors.Is(err, ErrNotResumable):
			// A promptless, unresumable worker is intentionally left terminated:
			// expected, not an operational failure, so log it quietly.
			m.logger.Warn("restore-all: session left terminated (nothing to resume)", "sessionID", rec.ID)
		case errors.Is(err, ErrNotFound):
			// The row was reaped between listing and relaunch (a stale id during
			// reconciliation): skip it and keep restoring the rest.
			m.logger.Warn("restore-all: session vanished before relaunch, skipping", "sessionID", rec.ID)
		default:
			m.logger.Error("restore-all: relaunch failed", "sessionID", rec.ID, "error", err)
		}
		return
	}

	// One-shot: drop the consumed marker so it never outlives one restart
	// (#2319). A still-live session re-acquires it at the next quit.
	if restoredWorkspaceProject {
		for _, row := range projectRows {
			if err := m.upsertWorkspaceProjectRowState(ctx, row, "active"); err != nil {
				m.logger.Warn("restore-all: marking workspace repo active failed", "sessionID", rec.ID, "repo", row.RepoName, "error", err)
			}
		}
	} else {
		if err := m.markSessionWorktreesActive(ctx, rows); err != nil {
			m.logger.Warn("restore-all: marking worktrees active failed", "sessionID", rec.ID, "error", err)
		}
		if err := m.store.DeleteSessionWorktrees(ctx, rec.ID); err != nil {
			m.logger.Warn("restore-all: delete restore marker failed", "sessionID", rec.ID, "error", err)
		}
	}
}
