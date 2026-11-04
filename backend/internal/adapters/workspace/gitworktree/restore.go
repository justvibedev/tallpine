package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"os"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// Restore re-attaches to an existing worktree for the session if one is still
// present, recreating the handle without disturbing its contents.
func (w *Workspace) Restore(ctx context.Context, cfg ports.WorkspaceConfig) (ports.WorkspaceInfo, error) {
	if err := validateConfig(cfg); err != nil {
		return ports.WorkspaceInfo{}, err
	}
	repo, err := w.repoPathForConfig(cfg)
	if err != nil {
		return ports.WorkspaceInfo{}, err
	}
	if err := w.enableWorktreeConfig(ctx, repo); err != nil {
		return ports.WorkspaceInfo{}, err
	}
	path, err := w.restorePath(cfg)
	if err != nil {
		return ports.WorkspaceInfo{}, err
	}
	records, err := w.listRecords(ctx, repo)
	if err != nil {
		return ports.WorkspaceInfo{}, err
	}
	// recreateBranch is the branch used if we fall through to recreate the
	// worktree below. It defaults to cfg.Branch (the "no prior registration"
	// case) but is overridden to the stale registration's own branch when one
	// is found, so a session sitting on a child branch is not silently
	// recreated on cfg.Branch (typically the root branch) instead.
	recreateBranch := cfg.Branch
	if rec, ok := findWorktree(records, path); ok {
		missing, err := registeredWorktreeDirMissing(rec)
		if err != nil {
			return ports.WorkspaceInfo{}, err
		}
		if !missing {
			branch := rec.Branch
			if branch == "" {
				branch = cfg.Branch
			}
			w.refreshOrchestratorWorktree(ctx, cfg, repo, path)
			return ports.WorkspaceInfo{Path: path, Branch: branch, BaseRef: cfg.BaseRef, SessionID: cfg.SessionID, ProjectID: cfg.ProjectID, RepoPath: repo}, nil
		}
		// The registration outlived its directory (issue #2775: a session's git
		// worktree registration and DB row survived a deletion that removed only
		// the directory). Fall through to recreate the worktree at path below,
		// on the registration's own branch (not cfg.Branch), instead of
		// returning a handle to a directory that does not exist, which
		// previously made `cd <path> || exit` in the tmux launch command exit
		// instantly with no diagnostic. addWorktree re-registers the stale path
		// itself via `worktree add --force`; the registration is left in place
		// until then.
		if rec.Branch != "" {
			recreateBranch = rec.Branch
		}
	}
	if nonEmpty, err := pathExistsNonEmpty(path); err != nil {
		return ports.WorkspaceInfo{}, err
	} else if nonEmpty {
		if cfg.Path == "" {
			return ports.WorkspaceInfo{}, fmt.Errorf("gitworktree: refusing to restore %q: path exists and is not a registered worktree", path)
		}
		if _, err := moveStrayPathAside(path); err != nil {
			return ports.WorkspaceInfo{}, err
		}
	}
	if err := w.validateBranch(ctx, repo, recreateBranch); err != nil {
		return ports.WorkspaceInfo{}, err
	}
	baseRef, err := w.addWorktree(ctx, repo, path, recreateBranch, cfg.BaseBranch, cfg.BaseRef, "", false)
	if err != nil {
		return ports.WorkspaceInfo{}, err
	}
	return ports.WorkspaceInfo{Path: path, Branch: recreateBranch, BaseRef: baseRef, SessionID: cfg.SessionID, ProjectID: cfg.ProjectID, RepoPath: repo}, nil
}

func (w *Workspace) existingWorktree(ctx context.Context, repo, path string, cfg ports.WorkspaceConfig) (ports.WorkspaceInfo, bool, error) {
	records, err := w.listRecords(ctx, repo)
	if err != nil {
		return ports.WorkspaceInfo{}, false, err
	}
	if rec, ok := findWorktree(records, path); ok {
		missing, err := registeredWorktreeDirMissing(rec)
		if err != nil {
			return ports.WorkspaceInfo{}, false, err
		}
		if missing {
			// Report "no existing worktree" so Create falls through to
			// addWorktree, which re-registers this stale path in one step via
			// `worktree add --force`.
			//
			// Unlike Restore, Create deliberately recreates on cfg.Branch and
			// not on rec.Branch. Restore is re-attaching to a live session
			// whose branch may have moved on past what Tallpine recorded, so the
			// registration is the better source of truth there; Create is
			// materializing a NEW session, where a registration at this path is
			// a leftover from a prior session of the same name and its branch
			// says nothing about what the caller asked for.
			return ports.WorkspaceInfo{}, false, nil
		}
		branch := rec.Branch
		if branch == "" {
			branch = cfg.Branch
		}
		return ports.WorkspaceInfo{Path: path, Branch: branch, SessionID: cfg.SessionID, ProjectID: cfg.ProjectID}, true, nil
	}
	return ports.WorkspaceInfo{}, false, nil
}

// registeredWorktreeDirMissing reports whether a git-registered worktree's
// directory no longer exists on disk. A worktree registration (and the
// session's DB row) can outlive its directory when something removes the path
// out of band of Tallpine's own teardown (issue #2775: session tallpine-78
// kept its branches and worktree registration but its directory was gone, so
// handing that path straight to the runtime made the tmux launch command's
// `cd <path> || exit` guard exit instantly with no diagnostic). When it reports
// true, the caller materializes a fresh worktree at the same path with
// `git worktree add --force` (see worktreeAddForce), which re-registers the
// path itself: nothing here removes or prunes a registration first.
//
// This is deliberately a pure observation. Recovering by first clearing the
// stale registration would mean either the repo-wide `git worktree prune`,
// which also drops the registration of every OTHER worktree of the repo whose
// directory git currently cannot see (verified against real git: two worktrees
// with their directories both deleted, one prune removes BOTH registrations,
// silently reintroducing the "recreated on the wrong branch" failure
// recreateBranch exists to prevent for a sibling session that never asked to be
// touched), or the target-specific `git worktree remove --force`, which is
// check-then-delete against this stat: if anything materializes the worktree
// between the two, the force-remove deletes a live worktree and any uncommitted
// agent work in it. `worktree add --force` has neither problem: it touches only
// this path's registration, and git refuses it outright when the directory
// exists and is non-empty, so a lost race fails loudly instead of destroying
// work.
//
// If rec is locked (`git worktree lock`), this returns ErrWorktreeLocked
// instead of reporting a recoverable registration. Verified against real git: a
// single `worktree add --force` refuses a missing-but-locked registration (it
// demands `-f -f`), and `git worktree prune` leaves such a registration in
// place too, so attempting recovery here would just relay an opaque git error
// downstream. Locking is an explicit operator signal not to touch a worktree,
// so recovering it automatically would be wrong even if git allowed it.
func registeredWorktreeDirMissing(rec worktreeRecord) (bool, error) {
	info, err := os.Stat(rec.Path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("gitworktree: stat registered worktree %q: %w", rec.Path, err)
		}
		if rec.Locked {
			return false, fmt.Errorf(
				"%w: %q (branch %q) is registered but its directory is missing; unlock it (`git worktree unlock %s`) and retry, or remove the registration manually",
				ErrWorktreeLocked, rec.Path, rec.Branch, rec.Path,
			)
		}
		return true, nil
	}
	if !info.IsDir() {
		return false, fmt.Errorf("gitworktree: registered worktree %q is not a directory", rec.Path)
	}
	return false, nil
}
