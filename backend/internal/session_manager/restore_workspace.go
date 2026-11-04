package sessionmanager

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func restorableWorktreeRows(rows []domain.SessionWorktreeRecord) []domain.SessionWorktreeRecord {
	out := make([]domain.SessionWorktreeRecord, 0, len(rows))
	for _, row := range rows {
		if row.State == "removed" || legacyRestorableWorktreeRow(row) {
			out = append(out, row)
		}
	}
	return out
}

func legacyRestorableWorktreeRow(row domain.SessionWorktreeRecord) bool {
	return row.State == "" && (row.PreservedRef != "" || row.RepoName == domain.RootWorkspaceRepoName)
}

func (m *Manager) markSessionWorktreesActive(ctx context.Context, rows []domain.SessionWorktreeRecord) error {
	for _, row := range rows {
		row.State = "active"
		row.PreservedRef = ""
		if err := m.store.UpsertSessionWorktree(ctx, row); err != nil {
			return err
		}
	}
	return nil
}

// refreshOrchestrator must be true only when no agent can be attached to the
// worktree (see ports.WorkspaceConfig.RefreshOrchestrator).
func (m *Manager) restoreSessionWorkspace(ctx context.Context, project domain.ProjectRecord, rec domain.SessionRecord, refreshOrchestrator bool) (ports.WorkspaceInfo, error) {
	if projectKindForSession(project, rec.ProjectID) != domain.ProjectKindWorkspace {
		recreated, err := workspacePathMissing(rec.Metadata.WorkspacePath)
		if err != nil {
			return ports.WorkspaceInfo{}, err
		}
		ws, err := m.workspace.Restore(ctx, ports.WorkspaceConfig{
			ProjectID:     rec.ProjectID,
			SessionID:     rec.ID,
			Kind:          rec.Kind,
			SessionPrefix: sessionPrefix(project),
			Branch:        rec.Metadata.Branch,
			BaseBranch:    project.Config.WorktreeBaseBranch(),
			BaseRef:       rec.Metadata.DiffBaseRef,
			Path:          rec.Metadata.WorkspacePath,

			RefreshOrchestrator: refreshOrchestrator,
		})
		if err != nil {
			return ports.WorkspaceInfo{}, err
		}
		if err := m.restoreAttachments(ctx, rec.ID, ws); err != nil {
			return ports.WorkspaceInfo{}, fmt.Errorf("restore attachments: %w", err)
		}
		if recreated {
			if err := m.provisionRecreatedWorkspace(ctx, project, ws.Path); err != nil {
				return ports.WorkspaceInfo{}, fmt.Errorf("setup recreated workspace: %w", err)
			}
		}
		return ws, nil
	}
	rows, err := m.workspaceProjectRestoreRows(ctx, project, rec)
	if err != nil {
		return ports.WorkspaceInfo{}, err
	}
	recreated := false
	for _, row := range rows {
		missing, statErr := workspacePathMissing(row.Path)
		if statErr != nil {
			return ports.WorkspaceInfo{}, statErr
		}
		recreated = recreated || missing
	}
	root, err := m.restoreWorkspaceProjectRows(ctx, rows)
	if err != nil {
		return ports.WorkspaceInfo{}, err
	}
	for _, row := range rows {
		if err := m.upsertWorkspaceProjectRowState(ctx, row, "active"); err != nil {
			return ports.WorkspaceInfo{}, fmt.Errorf("mark repo %s active: %w", row.RepoName, err)
		}
	}
	ws := workspaceInfoFromRepoInfo(root)
	if err := m.restoreAttachments(ctx, rec.ID, ws); err != nil {
		return ports.WorkspaceInfo{}, fmt.Errorf("restore attachments: %w", err)
	}
	if recreated {
		if err := m.provisionRecreatedWorkspace(ctx, project, ws.Path); err != nil {
			return ports.WorkspaceInfo{}, fmt.Errorf("setup recreated workspace: %w", err)
		}
	}
	return ws, nil
}

// provisionRecreatedWorkspace runs the project's Setup for a restore whose
// worktree was reclaimed. Only such a restore runs Setup, so only it extends
// a REST restore's budget, from now, by the Setup steps' limits; any other
// restore keeps the request's ordinary budget.
func (m *Manager) provisionRecreatedWorkspace(ctx context.Context, project domain.ProjectRecord, workspacePath string) error {
	ctx, cancel := ports.ExtendSpawnRequestBudget(ctx, postCreateBudget(0, project.Config.PostCreate))
	defer cancel()
	return m.provisionWorkspace(ctx, project, workspacePath)
}

func workspacePathMissing(path string) (bool, error) {
	_, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	return false, err
}

func (m *Manager) workspaceProjectRestoreRows(ctx context.Context, project domain.ProjectRecord, rec domain.SessionRecord) ([]ports.WorkspaceRepoInfo, error) {
	rows, err := m.store.ListSessionWorktrees(ctx, rec.ID)
	if err != nil {
		return nil, err
	}
	return m.workspaceProjectRestoreRowsFromMarkers(ctx, project, rec, rows)
}

func (m *Manager) workspaceProjectRestoreRowsFromMarkers(ctx context.Context, project domain.ProjectRecord, rec domain.SessionRecord, rows []domain.SessionWorktreeRecord) ([]ports.WorkspaceRepoInfo, error) {
	if len(rows) > 1 {
		return m.sessionWorktreeRowsToRepoInfos(ctx, project, rec, rows)
	}
	childRepos, err := m.store.ListWorkspaceRepos(ctx, project.ID)
	if err != nil {
		return nil, err
	}
	rootPath := rec.Metadata.WorkspacePath
	rootBranch := rec.Metadata.Branch
	var rootBaseSHA, rootBaseRef string
	if len(rows) == 1 && (rows[0].RepoName == "" || rows[0].RepoName == domain.RootWorkspaceRepoName) {
		rootPath = cmp.Or(rows[0].WorktreePath, rootPath)
		rootBranch = cmp.Or(rows[0].Branch, rootBranch)
		rootBaseSHA = rows[0].BaseSHA
		rootBaseRef = rows[0].BaseRef
	}
	out := []ports.WorkspaceRepoInfo{{
		RepoName:  domain.RootWorkspaceRepoName,
		RepoPath:  project.Path,
		Path:      rootPath,
		Branch:    rootBranch,
		BaseSHA:   rootBaseSHA,
		BaseRef:   rootBaseRef,
		SessionID: rec.ID,
		ProjectID: rec.ProjectID,
	}}
	for _, repo := range childRepos {
		out = append(out, ports.WorkspaceRepoInfo{
			RepoName:     repo.Name,
			RepoPath:     filepath.Join(project.Path, filepath.FromSlash(repo.RelativePath)),
			Path:         filepath.Join(rootPath, filepath.FromSlash(repo.RelativePath)),
			Branch:       rootBranch,
			SessionID:    rec.ID,
			ProjectID:    rec.ProjectID,
			RelativePath: repo.RelativePath,
		})
	}
	return out, nil
}

func (m *Manager) workspaceProjectRows(ctx context.Context, rec domain.SessionRecord) ([]ports.WorkspaceRepoInfo, bool, error) {
	rows, err := m.store.ListSessionWorktrees(ctx, rec.ID)
	if err != nil {
		return nil, false, err
	}
	if len(rows) <= 1 {
		return nil, false, nil
	}
	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return nil, false, err
	}
	if project.Kind.WithDefault() != domain.ProjectKindWorkspace {
		return nil, false, nil
	}
	infos, err := m.sessionWorktreeRowsToRepoInfos(ctx, project, rec, rows)
	if err != nil {
		return nil, false, err
	}
	return infos, true, nil
}

func (m *Manager) sessionWorktreeRowsToRepoInfos(ctx context.Context, project domain.ProjectRecord, rec domain.SessionRecord, rows []domain.SessionWorktreeRecord) ([]ports.WorkspaceRepoInfo, error) {
	childRepos, err := m.store.ListWorkspaceRepos(ctx, project.ID)
	if err != nil {
		return nil, err
	}
	repoPaths := map[string]string{domain.RootWorkspaceRepoName: project.Path}
	relPaths := map[string]string{}
	for _, repo := range childRepos {
		repoPaths[repo.Name] = filepath.Join(project.Path, filepath.FromSlash(repo.RelativePath))
		relPaths[repo.Name] = repo.RelativePath
	}
	out := make([]ports.WorkspaceRepoInfo, 0, len(rows))
	for _, row := range rows {
		repoPath := repoPaths[row.RepoName]
		if repoPath == "" {
			return nil, fmt.Errorf("session worktree row %q no longer matches workspace registry", row.RepoName)
		}
		out = append(out, ports.WorkspaceRepoInfo{
			RepoName:     row.RepoName,
			RepoPath:     repoPath,
			Path:         row.WorktreePath,
			Branch:       cmp.Or(row.Branch, rec.Metadata.Branch),
			BaseSHA:      row.BaseSHA,
			BaseRef:      row.BaseRef,
			CreationSHA:  row.CreationSHA,
			SessionID:    rec.ID,
			ProjectID:    rec.ProjectID,
			RelativePath: relPaths[row.RepoName],
		})
	}
	return out, nil
}

func (m *Manager) saveAndTeardownWorkspaceProject(ctx context.Context, rec domain.SessionRecord, rows []ports.WorkspaceRepoInfo) error {
	for _, row := range rows {
		ref, err := m.workspace.StashUncommitted(ctx, workspaceInfoFromRepoInfo(row))
		if err != nil {
			return fmt.Errorf("save %s repo %s: stash: %w", rec.ID, row.RepoName, err)
		}
		if err := m.store.UpsertSessionWorktree(ctx, domain.SessionWorktreeRecord{
			SessionID:    rec.ID,
			RepoName:     row.RepoName,
			Branch:       row.Branch,
			BaseSHA:      row.BaseSHA,
			BaseRef:      row.BaseRef,
			CreationSHA:  row.CreationSHA,
			WorktreePath: row.Path,
			PreservedRef: ref,
			State:        "removed",
		}); err != nil {
			return fmt.Errorf("save %s repo %s: upsert worktree row: %w", rec.ID, row.RepoName, err)
		}
	}
	if err := m.teardownReviewerTerminal(ctx, rec.ID); err != nil {
		return fmt.Errorf("save %s: teardown reviewer: %w", rec.ID, err)
	}
	if err := m.lcm.MarkTerminated(ctx, rec.ID); err != nil {
		return fmt.Errorf("save %s: mark terminated: %w", rec.ID, err)
	}
	handle := runtimeHandle(rec.Metadata)
	if handle.ID != "" {
		if err := m.runtime.Destroy(ctx, handle); err != nil {
			m.logger.Warn("save-teardown-all: runtime destroy failed", "sessionID", rec.ID, "error", err)
		}
	}
	rootDestroyed := false
	for i := len(rows) - 1; i >= 0; i-- {
		info := workspaceInfoFromRepoInfo(rows[i])
		if err := m.workspace.ForceDestroy(ctx, info); err != nil {
			m.logger.Warn("save-teardown-all: force destroy failed", "sessionID", rec.ID, "repo", rows[i].RepoName, "error", err)
		} else if info.Path == rec.Metadata.WorkspacePath {
			rootDestroyed = true
		}
	}
	if rootDestroyed {
		m.cleanupAgentWorkspace(ctx, rec, rec.Metadata.WorkspacePath)
	}
	return nil
}

func (m *Manager) destroyWorkspaceProjectRows(ctx context.Context, rows []ports.WorkspaceRepoInfo) (ports.WorkspaceReclaim, error) {
	touched := false
	aggregate := ports.WorkspaceReclaimAlreadyAbsent
	var firstErr error
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].Path == "" {
			continue
		}
		info := workspaceInfoFromRepoInfo(rows[i])
		reclaim := ports.WorkspaceReclaimRemoved
		var err error
		if reclaimer, ok := m.workspace.(ports.WorkspaceReclaimer); ok {
			reclaim, err = reclaimer.DestroyReclaim(ctx, info)
		} else {
			err = m.workspace.Destroy(ctx, info)
		}
		if err != nil {
			if errors.Is(err, ports.ErrWorkspaceDirty) {
				return aggregate, err
			}
			if stateErr := m.upsertWorkspaceProjectRowState(ctx, rows[i], "retry_remove"); stateErr != nil && firstErr == nil {
				firstErr = stateErr
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := m.upsertWorkspaceProjectRowState(ctx, rows[i], "unavailable"); err != nil && firstErr == nil {
			firstErr = err
		}
		touched = true
		if reclaim != ports.WorkspaceReclaimAlreadyAbsent {
			aggregate = ports.WorkspaceReclaimRemoved
		}
	}
	if !touched && firstErr == nil {
		return "", nil
	}
	return aggregate, firstErr
}

func (m *Manager) upsertWorkspaceProjectRowState(ctx context.Context, row ports.WorkspaceRepoInfo, state string) error {
	return m.store.UpsertSessionWorktree(ctx, domain.SessionWorktreeRecord{
		SessionID:    row.SessionID,
		RepoName:     row.RepoName,
		Branch:       row.Branch,
		BaseSHA:      row.BaseSHA,
		BaseRef:      row.BaseRef,
		CreationSHA:  row.CreationSHA,
		WorktreePath: row.Path,
		State:        state,
	})
}

func (m *Manager) restoreWorkspaceProjectRows(ctx context.Context, rows []ports.WorkspaceRepoInfo) (ports.WorkspaceRepoInfo, error) {
	var root ports.WorkspaceRepoInfo
	for _, row := range rows {
		restored, err := m.workspace.Restore(ctx, ports.WorkspaceConfig{
			ProjectID: row.ProjectID,
			SessionID: row.SessionID,
			Branch:    row.Branch,
			BaseRef:   row.BaseRef,
			RepoPath:  row.RepoPath,
			Path:      row.Path,
		})
		if err != nil {
			return ports.WorkspaceRepoInfo{}, fmt.Errorf("repo %s: %w", row.RepoName, err)
		}
		row.Path = restored.Path
		row.Branch = restored.Branch
		row.BaseRef = cmp.Or(restored.BaseRef, row.BaseRef)
		if row.RepoName == domain.RootWorkspaceRepoName {
			root = row
		}
	}
	if root.Path == "" {
		return ports.WorkspaceRepoInfo{}, errors.New("workspace project root worktree row missing")
	}
	return root, nil
}

func (m *Manager) applyWorkspaceProjectPreserved(ctx context.Context, rows []ports.WorkspaceRepoInfo) {
	for _, row := range rows {
		var preserveRef string
		sessionRows, err := m.store.ListSessionWorktrees(ctx, row.SessionID)
		if err != nil {
			m.logger.Error("restore-all: list worktrees failed", "sessionID", row.SessionID, "error", err)
			continue
		}
		for _, sessionRow := range sessionRows {
			if sessionRow.RepoName == row.RepoName {
				preserveRef = sessionRow.PreservedRef
				break
			}
		}
		if preserveRef == "" {
			continue
		}
		if applyErr := m.workspace.ApplyPreserved(ctx, workspaceInfoFromRepoInfo(row), preserveRef); applyErr != nil {
			if errors.Is(applyErr, ports.ErrPreservedConflict) {
				m.logger.Warn("restore-all: apply preserved produced conflicts; agent relaunched with conflict markers in place",
					"sessionID", row.SessionID, "repo", row.RepoName, "ref", preserveRef, "error", applyErr)
			} else {
				m.logger.Error("restore-all: apply preserved failed", "sessionID", row.SessionID, "repo", row.RepoName, "error", applyErr)
			}
		}
	}
}
