package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// DestroyWorkspaceProject removes worktrees children first, preserving any
// uncommitted changes even when a start is cancelled after publication.
func (w *Workspace) DestroyWorkspaceProject(ctx context.Context, info ports.WorkspaceProjectInfo) error {
	var firstErr error
	for i := len(info.Worktrees) - 1; i >= 0; i-- {
		wt := info.Worktrees[i]
		if wt.Path == "" {
			continue
		}
		repoPath := wt.RepoPath
		if repoPath == "" {
			if firstErr == nil {
				firstErr = fmt.Errorf("gitworktree: missing repo path for worktree %q", wt.Path)
			}
			continue
		}
		baseSHA := wt.CreationSHA
		if baseSHA == "" {
			baseSHA = wt.BaseSHA
		}
		err := w.Destroy(ctx, ports.WorkspaceInfo{Path: wt.Path, Branch: wt.Branch, BaseSHA: baseSHA, SessionID: wt.SessionID, ProjectID: wt.ProjectID, RepoPath: repoPath})
		if errors.Is(err, ports.ErrWorkspaceDirty) {
			return err
		}
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Destroy removes the session's worktree and prunes it from the repo, refusing
// (rather than force-deleting) if git still has the path registered afterwards.
func (w *Workspace) Destroy(ctx context.Context, info ports.WorkspaceInfo) error {
	_, err := w.destroy(ctx, info)
	return err
}

// DestroyReclaim is Destroy plus the reclaim outcome. A worktree directory that
// was already gone before this call leaves nothing for `git worktree remove` to
// operate on: the prune below still clears the stale registration and the final
// os.RemoveAll is a no-op on a missing path, so the whole teardown reports
// success without any disk having been released. Callers that count reclaimed
// workspaces need that told apart from a real removal.
func (w *Workspace) DestroyReclaim(ctx context.Context, info ports.WorkspaceInfo) (ports.WorkspaceReclaim, error) {
	return w.destroy(ctx, info)
}

// DeletePreparedBranch is called only after a speculative worktree is gone.
// A branch that moved beyond its original base belongs to the user and stays.
func (w *Workspace) DeletePreparedBranch(ctx context.Context, info ports.WorkspaceInfo) error {
	if info.Branch == "" || info.BaseSHA == "" {
		return nil
	}
	repo, err := w.repoPathForInfo(info)
	if err != nil {
		return err
	}
	if err := w.validateBranch(ctx, repo, info.Branch); err != nil {
		return err
	}
	unlock, err := repoTeardownLock(repo)
	if err != nil {
		return err
	}
	defer unlock()
	if err := w.requireReachableRepo(repo); err != nil {
		return err
	}
	branchRef := "refs/heads/" + info.Branch
	exists, err := w.refExists(ctx, repo, branchRef)
	if err != nil || !exists {
		return err
	}
	records, err := w.listRecords(ctx, repo)
	if err != nil {
		return err
	}
	if _, checkedOut := findWorktreeByBranch(records, info.Branch); checkedOut {
		return fmt.Errorf("gitworktree: prepared branch %q is still checked out", info.Branch)
	}
	tip, err := w.revParse(ctx, repo, branchRef)
	if err != nil {
		return err
	}
	if tip != info.BaseSHA {
		return nil
	}
	if _, err := w.run(ctx, w.binary, "-C", repo, "update-ref", "-d", branchRef, tip); err != nil {
		return fmt.Errorf("gitworktree: delete unmodified prepared branch %q: %w", info.Branch, err)
	}
	return nil
}

func (w *Workspace) destroy(ctx context.Context, info ports.WorkspaceInfo) (ports.WorkspaceReclaim, error) {
	if info.Path == "" {
		return ports.WorkspaceReclaimAlreadyAbsent, fmt.Errorf("%w: empty path", ErrUnsafePath)
	}
	repo, err := w.repoPathForInfo(info)
	if err != nil {
		return ports.WorkspaceReclaimAlreadyAbsent, err
	}
	path, err := w.validateManagedPath(info.Path)
	if err != nil {
		return ports.WorkspaceReclaimAlreadyAbsent, err
	}
	// Serialize this repo's teardown against other sessions of the same project
	// being killed concurrently; git worktree metadata is shared per repo.
	unlock, err := repoTeardownLock(repo)
	if err != nil {
		return ports.WorkspaceReclaimAlreadyAbsent, fmt.Errorf("gitworktree: repo path: %w", err)
	}
	defer unlock()
	// Sampled before any teardown step runs, so it reflects the state this call
	// found rather than the state it left behind. Only a definite absence counts
	// as already-absent; a stat that fails for any other reason (permissions, a
	// racing writer) stays on the removed path so an unknown is never reported
	// as "nothing to do".
	reclaim := ports.WorkspaceReclaimRemoved
	if _, statErr := os.Lstat(path); errors.Is(statErr, os.ErrNotExist) {
		reclaim = ports.WorkspaceReclaimAlreadyAbsent
	}
	if err := w.requireReachableRepo(repo); err != nil {
		return reclaim, err
	}
	// A speculative add may have left a nonempty directory before Git registered
	// it. Never treat that as a clean worktree just because it has no registration.
	if info.BaseSHA != "" {
		records, err := w.listRecords(ctx, repo)
		if err != nil {
			return reclaim, err
		}
		if _, registered := findWorktree(records, path); !registered {
			nonEmpty, err := pathExistsNonEmpty(path)
			if err != nil {
				return reclaim, err
			}
			if nonEmpty {
				return reclaim, fmt.Errorf("gitworktree: preserve unregistered partial worktree %q: %w", path, ports.ErrWorkspaceDirty)
			}
		}
	}
	// Move the directory aside rather than waiting out `git worktree remove`'s
	// walk of an ignored-file mountain; falls through to the git-driven path
	// below whenever the move is not clearly safe.
	if handled, err := w.discardWorktree(ctx, repo, path); handled {
		return reclaim, err
	}
	_, removeErr := w.run(ctx, w.binary, worktreeRemoveArgs(repo, path)...)
	if _, err := w.run(ctx, w.binary, worktreePruneArgs(repo)...); err != nil {
		return reclaim, fmt.Errorf("gitworktree: worktree prune: %w", err)
	}
	records, err := w.listRecords(ctx, repo)
	if err != nil {
		return reclaim, err
	}
	if _, ok := findWorktree(records, path); ok {
		if removeErr != nil {
			if isLockedWorktreeRemoveError(removeErr) {
				return reclaim, fmt.Errorf("gitworktree: refusing to remove %q: path is still registered after git worktree prune (worktree remove: %w)", path, removeErr)
			}
			// Distinguish the dirty-worktree refusal (uncommitted agent work)
			// from other registration leftovers (e.g. a locked worktree) so the
			// Session Manager can preserve the workspace without erroring.
			// isDirty ignores untracked node_modules symlinks that `git worktree
			// remove` still refuses on; that case (the discard path could not move
			// the directory) falls through to the still-registered error below.
			dirty, statusErr := w.isDirty(ctx, path)
			if statusErr == nil && dirty {
				return reclaim, fmt.Errorf("gitworktree: refusing to remove %q: %w (worktree remove: %w)", path, ports.ErrWorkspaceDirty, removeErr)
			}
			if statusErr != nil {
				// A failed probe must stay visible: without it the caller can't
				// tell "not dirty" from "couldn't check".
				return reclaim, fmt.Errorf("gitworktree: refusing to remove %q: path is still registered after git worktree prune (worktree remove: %w; dirty probe: %w)", path, removeErr, statusErr)
			}
			return reclaim, fmt.Errorf("gitworktree: refusing to remove %q: path is still registered after git worktree prune (worktree remove: %w)", path, removeErr)
		}
		return reclaim, fmt.Errorf("gitworktree: refusing to remove %q: path is still registered after git worktree prune", path)
	}
	if err := removeAllWithRetry(ctx, path); err != nil {
		if errors.Is(err, errRemoveRetryExhausted) {
			return reclaim, fmt.Errorf("gitworktree: remove unregistered path %q: %w (cause: %w)", path, ports.ErrWorkspaceDeferred, err)
		}
		return reclaim, fmt.Errorf("gitworktree: remove unregistered path %q: %w", path, err)
	}
	return reclaim, nil
}

// ForceDestroy removes the session's worktree unconditionally (--force), prunes
// it from git's worktree list, and falls back to os.RemoveAll if any filesystem
// residue remains.
//
// ponytail: only safe to call AFTER the session's uncommitted work has been
// captured via StashUncommitted. Calling it before capture silently
// discards agent work. For interactive teardown (tallpine session kill, tallpine cleanup)
// use Destroy, which refuses dirty worktrees via ErrWorkspaceDirty.
func (w *Workspace) ForceDestroy(ctx context.Context, info ports.WorkspaceInfo) error {
	if info.Path == "" {
		return fmt.Errorf("%w: empty path", ErrUnsafePath)
	}
	repo, err := w.repoPathForInfo(info)
	if err != nil {
		return err
	}
	path, err := w.validateManagedPath(info.Path)
	if err != nil {
		return err
	}
	unlock, err := repoTeardownLock(repo)
	if err != nil {
		return fmt.Errorf("gitworktree: repo path: %w", err)
	}
	defer unlock()
	if err := w.requireReachableRepo(repo); err != nil {
		return err
	}
	// Force teardown has no refusal to honour, so the move is unconditional:
	// rename the directory out of the way, drop the registration, unlink in the
	// background. This runs on daemon shutdown and orchestrator replacement,
	// which stalled on the same unlink that used to stall a kill.
	if discarded, moved := w.discard(path); moved {
		// A failed prune must leave both halves intact. Deleting anyway would
		// report failure while destroying the directory and stranding its
		// registration, and that dangling entry blocks the path from being
		// used again; restoring lets the caller retry against the state it
		// started from.
		if _, err := w.run(ctx, w.binary, worktreePruneArgs(repo)...); err != nil {
			w.undiscard(discarded, path)
			return fmt.Errorf("gitworktree: worktree prune: %w", err)
		}
		w.removeInBackground(discarded)
		return nil
	}
	// --force bypasses git's dirty check; errors here are advisory (the path may
	// already be gone). We proceed to prune regardless.
	_, _ = w.run(ctx, w.binary, worktreeForceRemoveArgs(repo, path)...)
	if _, err := w.run(ctx, w.binary, worktreePruneArgs(repo)...); err != nil {
		return fmt.Errorf("gitworktree: worktree prune: %w", err)
	}
	// os.RemoveAll as a backstop: cleans up filesystem residue left behind if
	// git worktree remove --force still left the directory (e.g. files outside
	// git tracking).
	if err := removeAllWithRetry(ctx, path); err != nil {
		return fmt.Errorf("gitworktree: force remove path %q: %w", path, err)
	}
	return nil
}

func (w *Workspace) forceDestroyPath(ctx context.Context, repo, path string) error {
	unlock, err := repoTeardownLock(repo)
	if err != nil {
		return fmt.Errorf("gitworktree: repo path: %w", err)
	}
	defer unlock()
	if err := w.requireReachableRepo(repo); err != nil {
		return err
	}
	// Force teardown has no refusal to honour, so the move is unconditional:
	// rename the directory out of the way, drop the registration, unlink in the
	// background.
	if discarded, ok := w.discard(path); ok {
		if err := w.pruneWorktrees(ctx, repo); err != nil {
			w.undiscard(discarded, path)
			return err
		}
		w.removeInBackground(discarded)
		return nil
	}
	_, _ = w.run(ctx, w.binary, worktreeForceRemoveArgs(repo, path)...)
	if err := w.pruneWorktrees(ctx, repo); err != nil {
		return err
	}
	if err := removeAllWithRetry(ctx, path); err != nil {
		return fmt.Errorf("gitworktree: force remove path %q: %w", path, err)
	}
	return nil
}

func (w *Workspace) pruneWorktrees(ctx context.Context, repo string) error {
	if _, err := w.run(ctx, w.binary, worktreePruneArgs(repo)...); err != nil {
		return fmt.Errorf("gitworktree: worktree prune: %w", err)
	}
	return nil
}

func isMissingRegisteredWorktreeError(err error) bool {
	return strings.Contains(err.Error(), "is a missing but already registered worktree")
}

func isLockedWorktreeRemoveError(err error) bool {
	return strings.Contains(err.Error(), "cannot remove a locked working tree")
}
