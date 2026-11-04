package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/gitdefault"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/textutil"
)

// CreateWorkspaceProject materialises a root-as-repo workspace session: the
// parent repo worktree is created at the session root, then each registered
// child repo is created at its relative path inside that root. All repos share
// one branch name; if the requested branch already exists in any repo, one
// suffixed branch that is free in every repo is selected and used everywhere.
func (w *Workspace) CreateWorkspaceProject(ctx context.Context, cfg ports.WorkspaceProjectConfig) (ports.WorkspaceProjectInfo, error) {
	if err := validateWorkspaceProjectConfig(cfg); err != nil {
		return ports.WorkspaceProjectInfo{}, err
	}
	rootRepo, err := physicalAbs(cfg.RootRepoPath)
	if err != nil {
		return ports.WorkspaceProjectInfo{}, fmt.Errorf("gitworktree: root repo path: %w", err)
	}
	if err := w.enableWorktreeConfig(ctx, rootRepo); err != nil {
		return ports.WorkspaceProjectInfo{}, err
	}
	rootPath, err := w.managedPath(ports.WorkspaceConfig{
		ProjectID:     cfg.ProjectID,
		SessionID:     cfg.SessionID,
		Kind:          cfg.Kind,
		SessionPrefix: cfg.SessionPrefix,
		Branch:        textutil.FirstTrimmed(cfg.Branch, defaultSessionBranchName(cfg.SessionID)),
	})
	if err != nil {
		return ports.WorkspaceProjectInfo{}, err
	}
	repos := make([]workspaceProjectRepo, 0, len(cfg.Repos)+1)
	repos = append(repos, workspaceProjectRepo{
		name:       domain.RootWorkspaceRepoName,
		repoPath:   rootRepo,
		outputPath: rootPath,
		baseBranch: cfg.BaseBranch,
	})
	for _, child := range cfg.Repos {
		repoPath, err := physicalAbs(child.RepoPath)
		if err != nil {
			return ports.WorkspaceProjectInfo{}, fmt.Errorf("gitworktree: child repo %q path: %w", child.Name, err)
		}
		if err := w.enableWorktreeConfig(ctx, repoPath); err != nil {
			return ports.WorkspaceProjectInfo{}, fmt.Errorf("gitworktree: child repo %q: %w", child.Name, err)
		}
		rel, err := cleanRelativePath(child.RelativePath)
		if err != nil {
			return ports.WorkspaceProjectInfo{}, fmt.Errorf("gitworktree: child repo %q: %w", child.Name, err)
		}
		outPath, err := w.validateManagedPath(filepath.Join(rootPath, filepath.FromSlash(rel)))
		if err != nil {
			return ports.WorkspaceProjectInfo{}, fmt.Errorf("gitworktree: child repo %q path: %w", child.Name, err)
		}
		repos = append(repos, workspaceProjectRepo{
			name:         child.Name,
			relativePath: rel,
			repoPath:     repoPath,
			outputPath:   outPath,
			baseBranch:   child.BaseBranch,
			baseRef:      child.BaseRef,
		})
	}
	requestedBranch := textutil.FirstTrimmed(cfg.Branch, defaultSessionBranchName(cfg.SessionID))
	existing := make(map[int]struct{}, len(repos))
	recovering := false
	for i, repo := range repos {
		records, err := w.listRecords(ctx, repo.repoPath)
		if err != nil {
			return ports.WorkspaceProjectInfo{}, err
		}
		record, ok := findWorktree(records, repo.outputPath)
		if !ok {
			continue
		}
		if record.Branch != requestedBranch {
			return ports.WorkspaceProjectInfo{}, fmt.Errorf(
				"gitworktree: managed path %q already contains branch %q, want %q",
				repo.outputPath, record.Branch, requestedBranch,
			)
		}
		recovering = true
		missing, err := registeredWorktreeDirMissing(record)
		if err != nil {
			return ports.WorkspaceProjectInfo{}, err
		}
		if !missing {
			existing[i] = struct{}{}
		}
	}
	branch := requestedBranch
	if !recovering {
		branch, err = w.workspaceProjectBranch(ctx, repos, requestedBranch, cfg.FreshBranch)
		if err != nil {
			return ports.WorkspaceProjectInfo{}, err
		}
	}
	// Resolve every repository base before creating the first worktree. Besides
	// keeping remote probing within one aggregate budget, this prevents a later
	// unresolved child from leaving session branches behind in earlier repos.
	remoteCtx, cancelRemote := context.WithTimeout(ctx, defaultBranchResolutionBudget)
	defer cancelRemote()
	for i := range repos {
		if i == 0 {
			refs, err := w.resolveWorkspaceRootRefs(ctx, repos[i].repoPath, repos[i].baseBranch)
			if err != nil {
				return ports.WorkspaceProjectInfo{}, fmt.Errorf("gitworktree: resolve workspace repo %q base: %w", repos[i].name, err)
			}
			repos[i].seedRef = refs.seedRef
			repos[i].baseRef = refs.baseRef
			continue
		}
		if repos[i].baseRef != "" {
			repos[i].seedRef = repos[i].baseRef
			requestedRef := "origin/" + branch
			if exists, err := w.refExists(ctx, repos[i].repoPath, requestedRef); err != nil {
				return ports.WorkspaceProjectInfo{}, fmt.Errorf("gitworktree: inspect workspace repo %q requested branch: %w", repos[i].name, err)
			} else if exists {
				repos[i].seedRef = requestedRef
			}
			continue
		}
		refs, err := w.resolveWorktreeRefs(ctx, remoteCtx, repos[i].repoPath, branch, repos[i].baseBranch)
		if err != nil {
			return ports.WorkspaceProjectInfo{}, fmt.Errorf("gitworktree: resolve workspace repo %q base: %w", repos[i].name, err)
		}
		repos[i].seedRef = refs.seedRef
		repos[i].baseRef = refs.baseRef
	}
	created := make([]workspaceProjectRepo, 0, len(repos))
	touched := make([]ports.WorkspaceRepoInfo, 0, len(repos))
	out := ports.WorkspaceProjectInfo{Worktrees: make([]ports.WorkspaceRepoInfo, 0, len(repos))}
	fail := func(cause error) (ports.WorkspaceProjectInfo, error) {
		if cfg.FreshBranch {
			return w.rollbackWorkspaceProjectPreparation(ctx, out, touched, cause)
		}
		for i := len(created) - 1; i >= 0; i-- {
			_ = w.forceDestroyPath(ctx, created[i].repoPath, created[i].outputPath)
		}
		return ports.WorkspaceProjectInfo{}, cause
	}
	for repoIndex, repo := range repos {
		creationSHA := ""
		if cfg.FreshBranch {
			localBranch, err := w.refExists(ctx, repo.repoPath, "refs/heads/"+branch)
			if err != nil {
				return fail(err)
			}
			if localBranch {
				// A recovered branch may already contain user commits. Only a SHA
				// durably captured by its original creation can authorize deletion.
				creationSHA = ""
			} else {
				creationSHA, err = w.revParse(ctx, repo.repoPath, repo.seedRef)
				if err != nil {
					return fail(err)
				}
			}
		}
		info := ports.WorkspaceRepoInfo{
			RepoName: repo.name, RepoPath: repo.repoPath, Path: repo.outputPath,
			Branch: branch, CreationSHA: creationSHA, BaseRef: repo.baseRef,
			SessionID: cfg.SessionID, ProjectID: cfg.ProjectID, RelativePath: repo.relativePath,
		}
		baseSHA, err := w.revParse(ctx, repo.repoPath, repo.baseRef)
		if err == nil {
			info.BaseSHA = baseSHA
			if _, ok := existing[repoIndex]; !ok {
				if cfg.FreshBranch {
					touched = append(touched, info)
				}
				baseSHA, err = w.createWorkspaceProjectRepo(ctx, repo, branch)
				if err == nil {
					created = append(created, repo)
				}
			}
		}
		if err != nil {
			return fail(err)
		}
		info.BaseSHA = baseSHA
		out.Worktrees = append(out.Worktrees, info)
		if repo.name == domain.RootWorkspaceRepoName {
			rootSHA := baseSHA
			if cfg.FreshBranch {
				rootSHA = creationSHA
			}
			out.Root = ports.WorkspaceInfo{Path: repo.outputPath, Branch: branch, BaseSHA: rootSHA, BaseRef: repo.baseRef, SessionID: cfg.SessionID, ProjectID: cfg.ProjectID}
		}
		if repoIndex == 0 {
			_, rootAlreadyExisted := existing[repoIndex]
			if !rootAlreadyExisted {
				if err := w.copyWorkspaceAssets(ctx, rootRepo, rootPath, cfg.Assets); err != nil {
					return fail(err)
				}
			}
		}
	}
	return out, nil
}

func (w *Workspace) rollbackWorkspaceProjectPreparation(ctx context.Context, out ports.WorkspaceProjectInfo, touched []ports.WorkspaceRepoInfo, cause error) (ports.WorkspaceProjectInfo, error) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	keep := make(map[string]bool, len(out.Worktrees)+len(touched))
	for _, row := range out.Worktrees {
		keep[row.Path] = true // recovered worktrees are not ours to roll back
	}
	for i := len(touched) - 1; i >= 0; i-- {
		row := touched[i]
		info := ports.WorkspaceInfo{Path: row.Path, Branch: row.Branch, BaseSHA: row.CreationSHA, BaseRef: row.BaseRef, RepoPath: row.RepoPath, SessionID: row.SessionID, ProjectID: row.ProjectID}
		if _, err := w.rollbackPreparedAdd(cleanupCtx, info); err != nil {
			cause = errors.Join(cause, err)
			keep[row.Path] = true
			// A nested child can be ignored by its parent repo. If it is dirty,
			// the parent may look clean and remove the child's files, so do not
			// continue teardown toward the root after any child failure.
			break
		}
		delete(keep, row.Path)
	}
	remaining := ports.WorkspaceProjectInfo{Worktrees: make([]ports.WorkspaceRepoInfo, 0, len(keep))}
	for _, row := range append(out.Worktrees, touched...) {
		if !keep[row.Path] {
			continue
		}
		remaining.Worktrees = append(remaining.Worktrees, row)
		delete(keep, row.Path)
		if row.RepoName == domain.RootWorkspaceRepoName {
			remaining.Root = ports.WorkspaceInfo{Path: row.Path, Branch: row.Branch, BaseSHA: row.CreationSHA, BaseRef: row.BaseRef, RepoPath: row.RepoPath, SessionID: row.SessionID, ProjectID: row.ProjectID}
		}
	}
	return remaining, cause
}

func (w *Workspace) resolveWorkspaceRootRefs(ctx context.Context, repo, configuredBranch string) (worktreeRefs, error) {
	branch := strings.TrimSpace(configuredBranch)
	if branch == "" {
		if out, err := w.run(ctx, w.binary, "-C", repo, "config", "--local", "--get", gitdefault.ManagedDefaultConfigKey); err == nil {
			branch = strings.TrimSpace(string(out))
		}
	}
	if branch == "" {
		return w.repairWorkspaceRootDefault(ctx, repo)
	}
	return w.resolveWorktreeRefsFromDefault(ctx, repo, "", branch)
}

func (w *Workspace) repairWorkspaceRootDefault(ctx context.Context, repo string) (worktreeRefs, error) {
	out, err := w.run(ctx, w.binary, "-C", repo, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return worktreeRefs{}, err
	}
	branch := strings.TrimSpace(string(out))
	if err := w.validateBranch(ctx, repo, branch); err != nil {
		return worktreeRefs{}, err
	}
	ref := "refs/heads/" + branch
	exists, err := w.refExists(ctx, repo, ref)
	if err != nil {
		return worktreeRefs{}, err
	}
	if !exists {
		return worktreeRefs{}, fmt.Errorf("workspace root local branch %q has no commit", branch)
	}
	unlock, err := repoConfigLock(repo)
	if err != nil {
		return worktreeRefs{}, err
	}
	_, err = w.run(ctx, w.binary, "-C", repo, "config", "--local", gitdefault.ManagedDefaultConfigKey, branch)
	unlock()
	if err != nil {
		return worktreeRefs{}, err
	}
	return worktreeRefs{seedRef: ref, baseRef: ref}, nil
}

type workspaceProjectRepo struct {
	name         string
	relativePath string
	repoPath     string
	outputPath   string
	baseBranch   string
	seedRef      string
	baseRef      string
}

func (w *Workspace) workspaceProjectBranch(ctx context.Context, repos []workspaceProjectRepo, requested string, fresh bool) (string, error) {
	branch := strings.TrimSpace(requested)
	if branch == "" {
		return "", errors.New("gitworktree: branch is required")
	}
	for i := 0; i < 100; i++ {
		candidate := branch
		if i > 0 {
			candidate = fmt.Sprintf("%s-%d", branch, i+1)
		}
		free, err := w.workspaceProjectBranchFree(ctx, repos, candidate, fresh)
		if err != nil {
			return "", err
		}
		if free {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("gitworktree: could not find free workspace branch for %q", branch)
}

func (w *Workspace) workspaceProjectBranchFree(ctx context.Context, repos []workspaceProjectRepo, branch string, fresh bool) (bool, error) {
	for _, repo := range repos {
		if err := w.validateBranch(ctx, repo.repoPath, branch); err != nil {
			return false, err
		}
		exists, err := w.branchNameTaken(ctx, repo.repoPath, branch, fresh)
		if err != nil {
			return false, err
		}
		if exists {
			return false, nil
		}
		records, err := w.listRecords(ctx, repo.repoPath)
		if err != nil {
			return false, err
		}
		if conflict, ok := findWorktreeByBranch(records, branch); ok && filepath.Clean(conflict.Path) != filepath.Clean(repo.outputPath) {
			return false, nil
		}
	}
	return true, nil
}

func (w *Workspace) branchNameTaken(ctx context.Context, repo, branch string, includeRemotes bool) (bool, error) {
	exists, err := w.refExists(ctx, repo, "refs/heads/"+branch)
	if err != nil || exists || !includeRemotes {
		return exists, err
	}
	out, err := w.run(ctx, w.binary, "-C", repo, "for-each-ref", "--format=%(refname)", "refs/remotes")
	if err != nil {
		return false, fmt.Errorf("gitworktree: list remote branches: %w", err)
	}
	for _, ref := range strings.Fields(string(out)) {
		if strings.HasSuffix(ref, "/"+branch) {
			return true, nil
		}
	}
	return false, nil
}

func (w *Workspace) createWorkspaceProjectRepo(ctx context.Context, repo workspaceProjectRepo, branch string) (string, error) {
	baseRef := strings.TrimSpace(repo.baseRef)
	if baseRef == "" {
		return "", errors.New("gitworktree: workspace repository base was not resolved before creation")
	}
	seedRef := textutil.FirstTrimmed(strings.TrimSpace(repo.seedRef), baseRef)
	baseSHA, err := w.revParse(ctx, repo.repoPath, baseRef)
	if err != nil {
		return "", err
	}
	// addWorktree handles both the ordinary new-branch path and a preparation
	// recovered after a daemon crash left this branch or registration behind.
	if _, err := w.addWorktree(ctx, repo.repoPath, repo.outputPath, branch, repo.baseBranch, baseRef, seedRef, false); err != nil {
		return "", fmt.Errorf("gitworktree: workspace repo %q worktree add branch %q from %q: %w", repo.name, branch, seedRef, err)
	}
	return baseSHA, nil
}
