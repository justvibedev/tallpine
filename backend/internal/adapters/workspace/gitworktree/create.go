package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// Create adds a git worktree for the session under the managed root, checking
// out the requested branch, and returns where it landed.
func (w *Workspace) Create(ctx context.Context, cfg ports.WorkspaceConfig) (ports.WorkspaceInfo, error) {
	if err := validateConfig(cfg); err != nil {
		return ports.WorkspaceInfo{}, err
	}
	repo, err := w.repoPath(cfg.ProjectID)
	if err != nil {
		return ports.WorkspaceInfo{}, err
	}
	if err := w.enableWorktreeConfig(ctx, repo); err != nil {
		return ports.WorkspaceInfo{}, err
	}
	if err := w.validateBranch(ctx, repo, cfg.Branch); err != nil {
		return ports.WorkspaceInfo{}, err
	}
	if cfg.FreshBranch {
		requested := cfg.Branch
		for suffix := 2; ; suffix++ {
			exists, err := w.branchNameTaken(ctx, repo, cfg.Branch, true)
			if err != nil {
				return ports.WorkspaceInfo{}, err
			}
			if !exists {
				break
			}
			if suffix > 100 {
				return ports.WorkspaceInfo{}, fmt.Errorf("gitworktree: no unused branch for %q", cfg.Branch)
			}
			cfg.Branch = fmt.Sprintf("%s-%d", requested, suffix)
		}
	}
	path, err := w.managedPath(cfg)
	if err != nil {
		return ports.WorkspaceInfo{}, err
	}
	if info, ok, err := w.existingWorktree(ctx, repo, path, cfg); err != nil {
		return ports.WorkspaceInfo{}, err
	} else if ok {
		if cfg.FreshBranch {
			return ports.WorkspaceInfo{}, fmt.Errorf("gitworktree: prepared workspace path %q already exists", path)
		}
		refs, err := w.resolveWorktreeRefsWithBudget(ctx, repo, cfg.Branch, cfg.BaseBranch)
		if err != nil {
			return ports.WorkspaceInfo{}, err
		}
		info.BaseRef = refs.baseRef
		w.refreshOrchestratorWorktree(ctx, cfg, repo, path)
		return info, nil
	}
	seedRef := ""
	seedSHA := ""
	if cfg.FreshBranch {
		refs, err := w.resolveWorktreeRefsWithBudget(ctx, repo, cfg.Branch, cfg.BaseBranch)
		if err != nil {
			return ports.WorkspaceInfo{}, err
		}
		cfg.BaseRef, seedRef = refs.baseRef, refs.seedRef
		seedSHA, err = w.revParse(ctx, repo, seedRef)
		if err != nil {
			return ports.WorkspaceInfo{}, err
		}
	}
	baseRef, err := w.addWorktree(ctx, repo, path, cfg.Branch, cfg.BaseBranch, cfg.BaseRef, seedRef, true)
	if err != nil {
		if cfg.FreshBranch {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			info := ports.WorkspaceInfo{Path: path, Branch: cfg.Branch, BaseSHA: seedSHA, BaseRef: cfg.BaseRef, SessionID: cfg.SessionID, ProjectID: cfg.ProjectID, RepoPath: repo}
			remaining, cleanupErr := w.rollbackPreparedAdd(cleanupCtx, info)
			if cleanupErr != nil {
				return remaining, errors.Join(err, cleanupErr)
			}
		}
		return ports.WorkspaceInfo{}, err
	}
	if err := w.seedWorktreeInclude(ctx, repo, path, cfg.CloneWorktreeInclude); err != nil {
		if cfg.FreshBranch {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			info := ports.WorkspaceInfo{Path: path, Branch: cfg.Branch, BaseSHA: seedSHA, BaseRef: baseRef, SessionID: cfg.SessionID, ProjectID: cfg.ProjectID, RepoPath: repo}
			if remaining, cleanupErr := w.rollbackPreparedAdd(cleanupCtx, info); cleanupErr != nil {
				return remaining, errors.Join(err, cleanupErr)
			}
		}
		return ports.WorkspaceInfo{}, err
	}
	var baseSHA string
	if cfg.FreshBranch {
		baseSHA = seedSHA
	}
	return ports.WorkspaceInfo{Path: path, Branch: cfg.Branch, BaseSHA: baseSHA, BaseRef: baseRef, SessionID: cfg.SessionID, ProjectID: cfg.ProjectID}, nil
}

// rollbackPreparedAdd cleans only the exact registered worktree, without
// forcing dirty files away, then deletes the branch only at its creation SHA.
func (w *Workspace) rollbackPreparedAdd(ctx context.Context, info ports.WorkspaceInfo) (ports.WorkspaceInfo, error) {
	records, err := w.listRecords(ctx, info.RepoPath)
	if err != nil {
		return info, err
	}
	if record, ok := findWorktree(records, info.Path); ok {
		if record.Branch != info.Branch {
			info.Path = ""
			return info, fmt.Errorf("gitworktree: prepared path is registered on another branch %q", record.Branch)
		}
		if err := w.Destroy(ctx, info); err != nil {
			return info, err
		}
	} else {
		nonEmpty, err := pathExistsNonEmpty(info.Path)
		if err != nil {
			return info, err
		}
		if nonEmpty {
			return info, fmt.Errorf("gitworktree: preserve unregistered partial worktree %q: %w", info.Path, ports.ErrWorkspaceDirty)
		}
		if err := os.Remove(info.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return info, err
		}
		info.Path = ""
	}
	if err := w.DeletePreparedBranch(ctx, info); err != nil {
		return info, err
	}
	return ports.WorkspaceInfo{}, nil
}

func (w *Workspace) addWorktree(ctx context.Context, repo, path, branch, baseBranch, baseRef, seedRef string, resolveExistingBase bool) (string, error) {
	// Refuse early if the branch is already checked out in another worktree:
	// `git worktree add` will fail, but its stderr leaks through as an opaque
	// 500. A typed sentinel lets the HTTP layer surface a 409.
	records, err := w.listRecords(ctx, repo)
	if err != nil {
		return "", err
	}
	if conflict, ok := findWorktreeByBranch(records, branch); ok && filepath.Clean(conflict.Path) != filepath.Clean(path) {
		return "", fmt.Errorf("%w: %q is checked out at %q", ErrBranchCheckedOutElsewhere, branch, conflict.Path)
	}
	// A registration at path whose directory is gone makes a plain add fail
	// ("is a missing but already registered worktree; use 'add -f' to
	// override"), so re-register it in the same add instead of clearing it
	// first. records is the freshly listed state, so this decision and the add
	// it feeds are as close together as git allows.
	force, err := staleRegistrationForPath(records, path)
	if err != nil {
		return "", err
	}

	localBranch, err := w.refExists(ctx, repo, "refs/heads/"+branch)
	if err != nil {
		return "", err
	}
	if localBranch {
		// Create needs a freshly resolved base for accurate initial diffs even
		// when the requested branch already exists. Restore already has durable
		// base metadata and only needs to reattach that branch; making it resolve
		// again can fail after remote metadata becomes unavailable.
		if resolveExistingBase {
			refs, err := w.resolveWorktreeRefsWithBudget(ctx, repo, branch, baseBranch)
			if err != nil {
				if errors.Is(err, errNoBaseRef) {
					return "", fmt.Errorf("%w: %q has no local head, no remote, and no tag — run `git fetch` then retry", ErrBranchNotFetched, branch)
				}
				return "", err
			}
			baseRef = refs.baseRef
		}
		if _, err := w.run(ctx, w.binary, worktreeAddBranchArgs(repo, path, branch, force)...); err != nil {
			return "", fmt.Errorf("gitworktree: worktree add existing branch %q: %w", branch, err)
		}
		return baseRef, nil
	}
	if baseRef == "" {
		refs, resolveErr := w.resolveWorktreeRefsWithBudget(ctx, repo, branch, baseBranch)
		err = resolveErr
		if err != nil {
			if errors.Is(err, errNoBaseRef) {
				return "", fmt.Errorf("%w: %q has no local head, no remote, and no tag — run `git fetch` then retry", ErrBranchNotFetched, branch)
			}
			return "", err
		}
		baseRef = refs.baseRef
		if err := w.addNewBranchWorktree(ctx, repo, branch, path, refs.seedRef, force); err != nil {
			return "", fmt.Errorf("gitworktree: worktree add branch %q from %q: %w", branch, refs.seedRef, err)
		}
		return baseRef, nil
	}
	if seedRef == "" {
		seedRef = baseRef
		requestedRef := "origin/" + branch
		if exists, err := w.refExists(ctx, repo, requestedRef); err != nil {
			return "", err
		} else if exists {
			seedRef = requestedRef
		}
	}

	// Restore reaches this path when its local branch is gone but its durable
	// comparison ref remains. Fresh creation returns above after resolving its
	// seed and comparison refs independently.
	if err := w.addNewBranchWorktree(ctx, repo, branch, path, seedRef, force); err != nil {
		return "", fmt.Errorf("gitworktree: worktree add branch %q from %q: %w", branch, seedRef, err)
	}
	return baseRef, nil
}

// staleRegistrationForPath reports whether records carries a registration for
// path whose directory is gone, i.e. whether an add at path needs git's
// `--force` override to re-register it. No registration at all is not stale.
func staleRegistrationForPath(records []worktreeRecord, path string) (bool, error) {
	rec, ok := findWorktree(records, path)
	if !ok {
		return false, nil
	}
	return registeredWorktreeDirMissing(rec)
}

// addNewBranchWorktree creates <branch> at <baseRef> and checks it out at
// <path>. It does what `git worktree add [--force] -b <branch> <path>
// <baseRef>` does, in git's own order (that form runs `git branch` first, then
// validates path and checks out), but as two commands so only the first holds
// the per-repository config lock: creating the branch records its upstream in
// the shared .git/config under git's branch.autoSetupMerge rules, and git
// fails, rather than waits for, a concurrent config writer. The checkout, the
// slow part, still runs in parallel with other spawns in the same repository.
//
// A stale registration at path (its directory vanished after the caller's own
// pre-check) makes the plain add fail with "is a missing but already registered
// worktree"; the retry re-registers it with `worktree add --force <path>
// <branch>`, git's documented override. A single --force still refuses a path
// that exists and is non-empty and a registration that is locked.
//
// Both callers reach this only after confirming refs/heads/<branch> did not
// exist (addWorktree via refExists, createWorkspaceProjectRepo via
// workspaceProjectBranchFree), and `git branch` refuses an existing branch, so
// the branch checked out here is always the one just created, never a
// pre-existing branch this would hijack.
//
// A checkout that fails outright leaves the created branch ref behind, as the
// single-command `-b` form does. That is deliberate: the failure that matters
// here is "path exists and is non-empty", which means something else
// materialized the worktree at path first, possibly checked out on this very
// branch, and deleting the ref would then damage the worktree that won. The
// leftover ref is harmless and self-correcting: the next addWorktree for it
// takes the existing-branch path, and workspaceProjectBranch simply picks the
// next free candidate.
func (w *Workspace) addNewBranchWorktree(ctx context.Context, repo, branch, path, baseRef string, force bool) error {
	if err := w.createBranch(ctx, repo, branch, baseRef); err != nil {
		return err
	}
	_, err := w.run(ctx, w.binary, worktreeAddBranchArgs(repo, path, branch, force)...)
	if err == nil {
		return nil
	}
	// --force was already in play, so the stale registration is not what failed.
	if force || !isMissingRegisteredWorktreeError(err) {
		return err
	}
	// Report the recovery failure alongside the original: on its own, the
	// original ("is a missing but already registered worktree") names the
	// condition recovery was FOR, not the reason recovery failed, and the two
	// are routinely different. The interesting ones are "'<path>' already
	// exists" (another restore materialized the worktree first, so this one
	// lost the race and must not be read as a stale registration) and "missing
	// but locked" (a registration that acquired a lock after the caller's
	// pre-check). Joining keeps errors.Is working for both.
	if _, retryErr := w.run(ctx, w.binary, worktreeAddBranchArgs(repo, path, branch, true)...); retryErr != nil {
		return errors.Join(err, retryErr)
	}
	return nil
}

// createBranch runs `git branch <branch> <baseRef>` under the repository's
// config lock (see addNewBranchWorktree).
func (w *Workspace) createBranch(ctx context.Context, repo, branch, baseRef string) error {
	unlock, err := repoConfigLock(repo)
	if err != nil {
		return err
	}
	defer unlock()
	_, err = w.run(ctx, w.binary, branchCreateArgs(repo, branch, baseRef)...)
	return err
}
