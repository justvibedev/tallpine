package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	tallpineprocess "tallpine.eu/tallpine/backend/internal/process"
)

type defaultBranchRefreshTarget struct {
	repoPath         string
	configuredBranch string
	resolved         ports.WorkspaceDefaultBranch
}

func (m *Manager) refreshDefaultBranchesBestEffort(ctx context.Context, project domain.ProjectRecord) map[string]string {
	if project.Kind.WithDefault() == domain.ProjectKindScratch {
		return nil
	}
	if strings.TrimSpace(project.Path) == "" {
		return nil
	}
	refresher, ok := m.workspace.(ports.WorkspaceDefaultBranchRefresher)
	if !ok {
		return nil
	}
	baseRefs := make(map[string]string)
	targets := []defaultBranchRefreshTarget{{
		repoPath: project.Path,
		// Translate the automatic-default sentinel before handing it to the
		// adapter. An empty branch tells the resolver to inspect this
		// repository's own remote HEAD; "auto" is not a Git branch name.
		configuredBranch: project.Config.WorktreeBaseBranch(),
	}}
	if project.Kind.WithDefault() == domain.ProjectKindWorkspace {
		repos, err := m.store.ListWorkspaceRepos(ctx, project.ID)
		if err != nil {
			m.logger.Warn("spawn: workspace child repo lookup failed; continuing with root refresh only", "projectID", project.ID, "error", err)
		} else {
			for _, repo := range repos {
				if repo.GitStatus == domain.GitStatusNeedsInit {
					continue
				}
				targets = append(targets, defaultBranchRefreshTarget{
					repoPath:         filepath.Join(project.Path, filepath.FromSlash(repo.RelativePath)),
					configuredBranch: repo.DefaultBranch,
				})
			}
		}
	}

	// Resolve every canonical ref before starting network I/O. This preserves
	// each repository's own inferred origin/HEAD even if an earlier fetch uses
	// the entire shared refresh budget.
	for i := range targets {
		resolved, err := refresher.ResolveDefaultBranch(ctx, targets[i].repoPath, targets[i].configuredBranch)
		if err != nil {
			m.logger.Warn("spawn: default branch resolution failed; continuing with adapter fallback",
				"projectID", project.ID,
				"repoPath", targets[i].repoPath,
				"defaultBranch", targets[i].configuredBranch,
				"error", err,
			)
			continue
		}
		targets[i].resolved = resolved
		if resolved.BaseRef != "" {
			baseRefs[filepath.Clean(targets[i].repoPath)] = resolved.BaseRef
		}
	}

	// One deadline covers the complete workspace refresh. A slow or offline
	// repository cannot multiply spawn latency by the number of child repos.
	fetchCtx, cancel := context.WithTimeout(ctx, m.defaultBranchRefreshTimeout)
	defer cancel()
	for _, target := range targets {
		if target.resolved.BaseRef == "" {
			continue
		}
		if err := refresher.FetchDefaultBranch(fetchCtx, target.repoPath, target.resolved); err != nil {
			m.logger.Warn("spawn: default branch refresh failed; continuing with local refs",
				"projectID", project.ID,
				"repoPath", target.repoPath,
				"remote", target.resolved.Remote,
				"branch", target.resolved.Branch,
				"baseRef", target.resolved.BaseRef,
				"error", err,
			)
		}
	}
	return baseRefs
}

func (m *Manager) createSessionWorkspace(ctx context.Context, project domain.ProjectRecord, cfg ports.SpawnConfig, id domain.SessionID, branch string, baseRefs map[string]string) (ports.WorkspaceInfo, *ports.WorkspaceProjectInfo, error) {
	projectKind := project.Kind.WithDefault()
	if cfg.ProjectID == "" {
		projectKind = domain.ProjectKindScratch
	}
	if projectKind != domain.ProjectKindWorkspace {
		baseBranch := project.Config.WorktreeBaseBranch()
		if projectKind == domain.ProjectKindScratch {
			baseBranch = ""
		}
		workspaceCfg := ports.WorkspaceConfig{
			ProjectID:     cfg.ProjectID,
			SessionID:     id,
			Kind:          cfg.Kind,
			SessionPrefix: laneWorkspacePrefix(project, cfg),
			Branch:        branch,
			FreshBranch:   cfg.TaskPreparation != "",
			BaseBranch:    baseBranch,
			BaseRef:       baseRefs[filepath.Clean(project.Path)],

			CloneWorktreeInclude: project.Config.CloneWorktreeInclude,
			RefreshOrchestrator:  true,
		}
		ws, err := m.workspace.Create(ctx, workspaceCfg)
		if errors.Is(err, ports.ErrWorkspaceBranchCheckedOutElsewhere) && cfg.Kind == domain.KindOrchestrator && cfg.Branch == "" {
			// Leave the occupied worktree intact; the adapter selects an unused suffix.
			workspaceCfg.FreshBranch = true
			ws, err = m.workspace.Create(ctx, workspaceCfg)
		}
		return ws, nil, err
	}
	workspaceProject, ok := m.workspace.(ports.WorkspaceProject)
	if !ok {
		return ports.WorkspaceInfo{}, nil, errors.New("workspace project materialization is not supported by workspace adapter")
	}
	repos, err := m.store.ListWorkspaceRepos(ctx, project.ID)
	if err != nil {
		return ports.WorkspaceInfo{}, nil, err
	}
	childRepos := make([]ports.WorkspaceProjectRepoConfig, 0, len(repos))
	assets := make([]ports.WorkspaceProjectAssetConfig, 0)
	for _, repo := range repos {
		if repo.GitStatus == domain.GitStatusNeedsInit {
			repoPath := filepath.Join(project.Path, filepath.FromSlash(repo.RelativePath))
			if _, err := os.Stat(filepath.Join(repoPath, ".git")); err == nil {
				continue
			}
			assets = append(assets, ports.WorkspaceProjectAssetConfig{
				RelativePath: repo.RelativePath,
				SourcePath:   repoPath,
			})
			continue
		}
		repoPath := filepath.Join(project.Path, filepath.FromSlash(repo.RelativePath))
		childRepos = append(childRepos, ports.WorkspaceProjectRepoConfig{
			Name:         repo.Name,
			RelativePath: repo.RelativePath,
			RepoPath:     repoPath,
			// Older rows may have captured the branch checked out during
			// registration. Automatic adapter fallback ignores it, while BaseRef
			// preserves the canonical target resolved before the refresh.
			BaseBranch: "",
			BaseRef:    baseRefs[filepath.Clean(repoPath)],
		})
	}
	info, err := workspaceProject.CreateWorkspaceProject(ctx, ports.WorkspaceProjectConfig{
		ProjectID:     cfg.ProjectID,
		SessionID:     id,
		Kind:          cfg.Kind,
		SessionPrefix: laneWorkspacePrefix(project, cfg),
		Branch:        branch,
		FreshBranch:   cfg.TaskPreparation != "",
		RootRepoPath:  project.Path,
		BaseBranch:    project.Config.WorktreeBaseBranch(),
		BaseRef:       baseRefs[filepath.Clean(project.Path)],
		Repos:         childRepos,
		Assets:        assets,
	})
	if err != nil && (cfg.TaskPreparation == "" || len(info.Worktrees) == 0) {
		return ports.WorkspaceInfo{}, nil, err
	}
	persistCtx := ctx
	if cfg.TaskPreparation != "" {
		var cancel context.CancelFunc
		persistCtx, cancel = spawnRollbackContext(ctx)
		defer cancel()
	}
	var previousRows []domain.SessionWorktreeRecord
	if cfg.TaskPreparation != "" {
		var readErr error
		previousRows, readErr = m.store.ListSessionWorktrees(persistCtx, id)
		if readErr != nil {
			return info.Root, &info, errors.Join(err, readErr)
		}
	}
	for i := range info.Worktrees {
		wt := &info.Worktrees[i]
		if cfg.TaskPreparation != "" && wt.CreationSHA == "" {
			for _, previous := range previousRows {
				if previous.RepoName == wt.RepoName && previous.Branch == wt.Branch &&
					previous.WorktreePath == wt.Path && previous.BaseRef == wt.BaseRef {
					wt.CreationSHA = previous.CreationSHA
					if wt.RepoName == domain.RootWorkspaceRepoName {
						info.Root.BaseSHA = previous.CreationSHA
					}
					break
				}
			}
		}
		if storeErr := m.store.UpsertSessionWorktree(persistCtx, domain.SessionWorktreeRecord{
			SessionID:    id,
			RepoName:     wt.RepoName,
			Branch:       wt.Branch,
			BaseSHA:      wt.BaseSHA,
			BaseRef:      wt.BaseRef,
			CreationSHA:  wt.CreationSHA,
			WorktreePath: wt.Path,
			State:        "active",
		}); storeErr != nil {
			if cfg.TaskPreparation != "" {
				return info.Root, &info, errors.Join(err, fmt.Errorf("record prepared workspace worktree %q: %w", wt.RepoName, storeErr))
			}
			cleanupCtx, cancel := spawnRollbackContext(ctx)
			cleanupErr := workspaceProject.DestroyWorkspaceProject(cleanupCtx, info)
			cancel()
			if cleanupErr != nil {
				return info.Root, &info, errors.Join(fmt.Errorf("record workspace worktree %q: %w", wt.RepoName, storeErr), cleanupErr)
			}
			return ports.WorkspaceInfo{}, nil, fmt.Errorf("record workspace worktree %q: %w", wt.RepoName, storeErr)
		}
	}
	return info.Root, &info, err
}

func resolveSpawnDiffBase(ctx context.Context, root, defaultBranch string) (string, string) {
	for _, ref := range spawnDiffBaseRefCandidates(defaultBranch) {
		if sha, ok := spawnGitSingleLine(ctx, root, "merge-base", "HEAD", ref); ok {
			return sha, ref
		}
	}
	if sha, ok := spawnGitSingleLine(ctx, root, "rev-parse", "HEAD"); ok {
		return sha, "HEAD"
	}
	return "", ""
}

func spawnDiffBaseRefCandidates(defaultBranch string) []string {
	defaultBranch = strings.TrimSpace(defaultBranch)
	if defaultBranch == "" {
		return nil
	}
	seen := map[string]struct{}{}
	var refs []string
	add := func(ref string) {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			return
		}
		if _, ok := seen[ref]; ok {
			return
		}
		seen[ref] = struct{}{}
		refs = append(refs, ref)
	}
	if !strings.HasPrefix(defaultBranch, "origin/") && !strings.HasPrefix(defaultBranch, "refs/") {
		add("origin/" + defaultBranch)
		add("refs/remotes/origin/" + defaultBranch)
	}
	add(defaultBranch)
	return refs
}

func spawnGitSingleLine(ctx context.Context, root string, args ...string) (string, bool) {
	cmd := tallpineprocess.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	value := strings.TrimSpace(string(out))
	return value, value != ""
}
