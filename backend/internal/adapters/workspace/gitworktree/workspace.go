package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	tallpineprocess "tallpine.eu/tallpine/backend/internal/process"
)

const (
	defaultGitBinary              = "git"
	defaultBranchResolutionBudget = 5 * time.Second
)

// ErrUnsafePath is returned when a resolved worktree path escapes the managed
// root (path traversal guard).
var (
	ErrUnsafePath = errors.New("gitworktree: unsafe workspace path")
)

// repoLocks hands out one mutex per canonical repository path. Different
// repositories are independent and stay unlocked, so unrelated projects still
// work in parallel.
type repoLocks struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// lock canonicalizes repo and returns the release for its mutex.
func (l *repoLocks) lock(repo string) (func(), error) {
	key, err := physicalAbs(repo)
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	m := l.locks[key]
	if m == nil {
		if l.locks == nil {
			l.locks = map[string]*sync.Mutex{}
		}
		m = &sync.Mutex{}
		l.locks[key] = m
	}
	l.mu.Unlock()
	m.Lock()
	return m.Unlock, nil
}

// teardownLocks serializes the repo-touching git worktree teardown sequences
// (destroy, ForceDestroy, forceDestroyPath) per repository. Each writes shared
// `.git/worktrees/*` metadata, and project removal now tears down all of a
// project's sessions concurrently, so interleaved prune/remove/list commands
// against one repo would race.
var teardownLocks repoLocks

// repoTeardownLock returns the release for the per-repository teardown lock a
// single git worktree teardown sequence must hold while it mutates the repo.
func repoTeardownLock(repo string) (func(), error) {
	return teardownLocks.lock(repo)
}

// configLocks serializes this daemon's writes to a repository's shared
// .git/config. Git takes config.lock for every write and fails a second writer
// outright ("could not lock config file") instead of waiting, so an
// orchestrator spawning many workers in one repository at once lost most of
// those spawns. Only the short config-writing commands hold it; checkouts run
// in parallel.
var configLocks repoLocks

// repoConfigLock returns the release for the per-repository lock every command
// that writes the shared .git/config must hold.
func repoConfigLock(repo string) (func(), error) {
	return configLocks.lock(repo)
}

// excludeLocks serializes the read-then-append of a repository's shared
// info/exclude, which concurrent spawns into one repository both update.
var excludeLocks repoLocks

// defaultBranchFetches coalesces concurrent refreshes of one repository's
// default branch. Spawns into a project run in parallel, and each refreshes the
// same remote branch first: parallel fetches would race on the remote-tracking
// ref's lock and repeat the same network round trip. A caller that arrives
// while a fetch is in flight shares its result.
var defaultBranchFetches singleflight.Group

// sharedDefaultBranchFetchTimeout bounds a coalesced default-branch fetch. The
// fetch runs detached from every caller's cancellation, so a caller that gives
// up (a closed task-preparation modal, an expired spawn budget) only stops its
// own wait and never kills the fetch another live spawn shares. A variable so
// tests can shorten it.
var sharedDefaultBranchFetchTimeout = 30 * time.Second

// ErrPreservedConflict is an adapter-local alias of ports.ErrPreservedConflict.
// Tests inside this package use this name; callers outside use ports.ErrPreservedConflict
// and errors.Is works because the adapter wraps the ports sentinel.
var ErrPreservedConflict = ports.ErrPreservedConflict

// ErrBranchCheckedOutElsewhere and ErrBranchNotFetched are adapter-local aliases
// of the port-level sentinels: they preserve the gitworktree-prefixed message
// while letting the service layer match on ports.ErrWorkspaceBranchCheckedOutElsewhere
// / ports.ErrWorkspaceBranchNotFetched without importing this package. Tests
// inside the adapter use these names; callers outside use the port sentinels.
var (
	ErrBranchCheckedOutElsewhere = ports.ErrWorkspaceBranchCheckedOutElsewhere
	ErrBranchNotFetched          = ports.ErrWorkspaceBranchNotFetched
	ErrBranchInvalid             = ports.ErrWorkspaceBranchInvalid
	ErrDefaultBranchUnresolved   = ports.ErrWorkspaceDefaultBranchUnresolved
	// ErrWorktreeLocked is an adapter-local alias of ports.ErrWorkspaceLocked,
	// following the same aliasing convention as the branch sentinels above.
	ErrWorktreeLocked = ports.ErrWorkspaceLocked
)

// RepoResolver maps a project to the absolute path of its source git repo.
type RepoResolver interface {
	RepoPath(projectID domain.ProjectID) (string, error)
}

// StaticRepoResolver is a RepoResolver backed by a fixed project→repo-path map.
type StaticRepoResolver map[domain.ProjectID]string

// RepoPath returns the configured repo path for a project, or an error if none
// is configured.
func (r StaticRepoResolver) RepoPath(projectID domain.ProjectID) (string, error) {
	path := r[projectID]
	if path == "" {
		return "", fmt.Errorf("gitworktree: no repo configured for project %q", projectID)
	}
	return path, nil
}

// Options configures a gitworktree Workspace. ManagedRoot and RepoResolver are
// required; Binary falls back to git from PATH.
type Options struct {
	Binary       string
	ManagedRoot  string
	RepoResolver RepoResolver
	// Logger receives the failures background removal cannot return to a
	// caller. Optional; nil silences them.
	Logger *slog.Logger
}

// Workspace creates per-session git worktrees under a managed root. It
// implements ports.Workspace.
type Workspace struct {
	binary      string
	managedRoot string
	repos       RepoResolver
	run         commandRunner
	logger      *slog.Logger
	// discards tracks background worktree removals so tests can wait for them.
	// Production never waits: that deferral is the point (see discard.go).
	discards sync.WaitGroup
}

type commandRunner func(ctx context.Context, binary string, args ...string) ([]byte, error)

var _ ports.Workspace = (*Workspace)(nil)

var _ ports.WorkspaceDefaultBranchRefresher = (*Workspace)(nil)

var _ ports.WorkspaceProject = (*Workspace)(nil)

var _ ports.WorkspaceObserver = (*Workspace)(nil)

var _ ports.WorkspaceReclaimer = (*Workspace)(nil)

var _ ports.WorkspacePreparationBranchCleaner = (*Workspace)(nil)

// New builds a gitworktree Workspace, validating that ManagedRoot and
// RepoResolver are set and resolving the root to an absolute, symlink-free path.
func New(opts Options) (*Workspace, error) {
	binary := opts.Binary
	if binary == "" {
		binary = defaultGitBinary
	}
	if opts.ManagedRoot == "" {
		return nil, errors.New("gitworktree: ManagedRoot is required")
	}
	if opts.RepoResolver == nil {
		return nil, errors.New("gitworktree: RepoResolver is required")
	}
	root, err := physicalAbs(opts.ManagedRoot)
	if err != nil {
		return nil, fmt.Errorf("gitworktree: managed root: %w", err)
	}
	w := &Workspace{
		binary:      binary,
		managedRoot: filepath.Clean(root),
		repos:       opts.RepoResolver,
		run:         runCommand,
		logger:      opts.Logger,
	}
	// A daemon that died mid-removal leaves directories under the discard root
	// that nothing else will ever look at again. Sweeping at construction is
	// the one moment we know the root is ours.
	w.sweepDiscarded()
	return w, nil
}

// ResolveDefaultBranch selects a canonical remote-tracking ref using only local
// repository state. A slash is treated as a remote qualifier only when its
// prefix exactly matches a configured remote; otherwise it remains part of an
// origin branch name such as release/2026.
func (w *Workspace) ResolveDefaultBranch(ctx context.Context, repoPath, configuredBranch string) (ports.WorkspaceDefaultBranch, error) {
	repo, err := physicalAbs(repoPath)
	if err != nil {
		return ports.WorkspaceDefaultBranch{}, fmt.Errorf("gitworktree: repo path: %w", err)
	}
	branch := strings.TrimSpace(configuredBranch)
	if branch == "" {
		resolution, err := w.resolveAnnounced(ctx, ctx, repo)
		if err != nil {
			return ports.WorkspaceDefaultBranch{}, fmt.Errorf("gitworktree: resolve repository default for %q: %w", repo, err)
		}
		return ports.WorkspaceDefaultBranch{
			Remote:  resolution.Remote,
			Branch:  resolution.Branch,
			BaseRef: resolution.Ref,
		}, nil
	}
	remote := "origin"
	qualifiedRemote := false
	if candidateRemote, candidateBranch, ok := strings.Cut(branch, "/"); ok && candidateRemote != "" && candidateBranch != "" {
		exists, err := w.remoteExists(ctx, repo, candidateRemote)
		if err != nil {
			return ports.WorkspaceDefaultBranch{}, err
		}
		if exists {
			remote = candidateRemote
			branch = candidateBranch
			qualifiedRemote = true
		}
	}
	if err := w.validateBranch(ctx, repo, branch); err != nil {
		return ports.WorkspaceDefaultBranch{}, err
	}
	remoteRef := "refs/remotes/" + remote + "/" + branch
	if exists, err := w.refExists(ctx, repo, remoteRef); err != nil {
		return ports.WorkspaceDefaultBranch{}, err
	} else if exists {
		return ports.WorkspaceDefaultBranch{
			Remote:  remote,
			Branch:  branch,
			BaseRef: remoteRef,
		}, nil
	}
	if !qualifiedRemote {
		localRef := "refs/heads/" + branch
		if exists, err := w.refExists(ctx, repo, localRef); err != nil {
			return ports.WorkspaceDefaultBranch{}, err
		} else if exists {
			return ports.WorkspaceDefaultBranch{
				Remote:  remote,
				Branch:  branch,
				BaseRef: localRef,
			}, nil
		}
	}
	return ports.WorkspaceDefaultBranch{
		Remote:  remote,
		Branch:  branch,
		BaseRef: remoteRef,
	}, nil
}

// FetchDefaultBranch refreshes the exact target returned by
// ResolveDefaultBranch using an explicit refspec.
func (w *Workspace) FetchDefaultBranch(ctx context.Context, repoPath string, target ports.WorkspaceDefaultBranch) error {
	repo, err := physicalAbs(repoPath)
	if err != nil {
		return fmt.Errorf("gitworktree: repo path: %w", err)
	}
	if strings.TrimSpace(target.Remote) == "" {
		return errors.New("gitworktree: remote is required")
	}
	if strings.TrimSpace(target.Branch) == "" {
		return errors.New("gitworktree: branch is required")
	}
	wantRef := "refs/remotes/" + target.Remote + "/" + target.Branch
	localRef := "refs/heads/" + target.Branch
	if target.BaseRef == localRef {
		return nil
	}
	if target.BaseRef != wantRef {
		return fmt.Errorf("gitworktree: invalid default branch target %q (want %q)", target.BaseRef, wantRef)
	}
	if err := w.validateBranch(ctx, repo, target.Branch); err != nil {
		return err
	}
	key := repo + "\x00" + target.Remote + "\x00" + target.Branch
	fetched := defaultBranchFetches.DoChan(key, func() (any, error) {
		fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sharedDefaultBranchFetchTimeout)
		defer cancel()
		_, err := w.run(fetchCtx, w.binary, fetchBranchArgs(repo, target.Remote, target.Branch)...)
		return nil, err
	})
	select {
	case res := <-fetched:
		if res.Err != nil {
			return fmt.Errorf("gitworktree: fetch %s %s: %w", target.Remote, target.Branch, res.Err)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("gitworktree: fetch %s %s: %w", target.Remote, target.Branch, ctx.Err())
	}
}

func (w *Workspace) remoteExists(ctx context.Context, repo, remote string) (bool, error) {
	out, err := w.run(ctx, w.binary, remoteListArgs(repo)...)
	if err != nil {
		return false, fmt.Errorf("gitworktree: list remotes: %w", err)
	}
	for _, candidate := range strings.Fields(string(out)) {
		if candidate == remote {
			return true, nil
		}
	}
	return false, nil
}

func runCommand(ctx context.Context, binary string, args ...string) ([]byte, error) {
	cmd := tallpineprocess.CommandContext(ctx, binary, args...)
	cmd.Env = os.Environ()
	if readsGitMessages(args) {
		cmd.Env = untranslatedGitEnv(cmd.Env)
	}
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=Never")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, commandError{args: append([]string{binary}, args...), output: string(out), err: err}
	}
	return out, nil
}

type commandError struct {
	args   []string
	output string
	err    error
}

func (e commandError) Error() string {
	if strings.TrimSpace(e.output) == "" {
		return fmt.Sprintf("%s: %v", strings.Join(e.args, " "), e.err)
	}
	return fmt.Sprintf("%s: %v: %s", strings.Join(e.args, " "), e.err, strings.TrimSpace(e.output))
}

func (e commandError) Unwrap() error { return e.err }
