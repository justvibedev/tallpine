package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"

	"tallpine.eu/tallpine/backend/internal/gitdefault"
)

func (w *Workspace) revParse(ctx context.Context, repo, ref string) (string, error) {
	out, err := w.run(ctx, w.binary, "-C", repo, "rev-parse", "--verify", ref)
	if err != nil {
		return "", fmt.Errorf("gitworktree: rev-parse %q: %w", ref, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (w *Workspace) validateBranch(ctx context.Context, repo, branch string) error {
	if _, err := w.run(ctx, w.binary, checkRefFormatBranchArgs(repo, branch)...); err != nil {
		return fmt.Errorf("%w: %q (%w)", ErrBranchInvalid, branch, err)
	}
	return nil
}

// errNoBaseRef is an internal sentinel: every candidate base ref is missing.
// addWorktree translates it into ErrBranchNotFetched.
var errNoBaseRef = errors.New("gitworktree: no base ref found")

// worktreeRefs keeps the ref used to seed a session branch separate from the
// repository-default ref used for diffs. A fetched session branch may be ahead
// of the default; returning it as the diff base would hide all of those commits.
type worktreeRefs struct {
	seedRef string
	baseRef string
}

func (w *Workspace) resolveWorktreeRefs(ctx, remoteCtx context.Context, repo, branch, baseBranch string) (worktreeRefs, error) {
	if strings.TrimSpace(baseBranch) != "" {
		return w.resolveWorktreeRefsFromDefault(ctx, repo, branch, baseBranch)
	}
	resolver := gitdefault.New(w.binary, gitdefault.Runner(w.run))
	remote, requestedRef, requestedExists, requestedErr := resolver.FindCachedRemoteBranch(ctx, repo, branch)
	if requestedErr != nil && !errors.Is(requestedErr, gitdefault.ErrUnresolved) {
		return worktreeRefs{}, fmt.Errorf("gitworktree: inspect requested branch for %q: %w", repo, requestedErr)
	}
	resolution, err := w.resolveAnnounced(ctx, remoteCtx, repo)
	if err != nil {
		if errors.Is(err, gitdefault.ErrUnresolved) {
			// A fetched requested branch remains a valid seed when the remote
			// exposes no default metadata. There is no authoritative comparison
			// ref in this case, so preserve the legacy fallback to that branch.
			if requestedExists {
				return worktreeRefs{seedRef: requestedRef, baseRef: requestedRef}, nil
			}
			return worktreeRefs{}, fmt.Errorf(
				"%w: %s; configure this repository's primary remote and cached HEAD (for example, `git -C %q remote set-head <remote> <branch>`) and retry",
				ErrDefaultBranchUnresolved, strings.TrimPrefix(err.Error(), gitdefault.ErrUnresolved.Error()+": "),
				repo,
			)
		}
		return worktreeRefs{}, fmt.Errorf("gitworktree: resolve repository default for %q: %w", repo, err)
	}
	slog.Debug("gitworktree: resolved automatic default branch",
		"repo", repo,
		"remote", resolution.Remote,
		"branch", resolution.Branch,
		"source", resolution.Source,
	)

	if exists, err := w.refExists(ctx, repo, resolution.Ref); err != nil {
		return worktreeRefs{}, err
	} else if exists {
		seedRef := resolution.Ref
		if requestedExists {
			slog.Debug("gitworktree: resolved requested remote branch",
				"repo", repo,
				"remote", remote,
				"branch", branch,
			)
			seedRef = requestedRef
		}
		return worktreeRefs{seedRef: seedRef, baseRef: resolution.Ref}, nil
	}
	return worktreeRefs{}, fmt.Errorf("%w: resolved default branch %q from %q, but ref %q is unavailable", errNoBaseRef, resolution.Branch, resolution.Source, resolution.Ref)
}

func (w *Workspace) resolveWorktreeRefsWithBudget(ctx context.Context, repo, branch, baseBranch string) (worktreeRefs, error) {
	remoteCtx, cancelRemote := context.WithTimeout(ctx, defaultBranchResolutionBudget)
	defer cancelRemote()
	return w.resolveWorktreeRefs(ctx, remoteCtx, repo, branch, baseBranch)
}

func (w *Workspace) resolveWorktreeRefsFromDefault(ctx context.Context, repo, branch, defaultBranch string) (worktreeRefs, error) {
	requestedRef := "origin/" + branch
	requestedExists, err := w.refExists(ctx, repo, requestedRef)
	if err != nil {
		return worktreeRefs{}, err
	}
	baseCandidates := configuredBaseRefCandidates(defaultBranch)
	for _, ref := range baseCandidates {
		exists, err := w.refExists(ctx, repo, ref)
		if err != nil {
			return worktreeRefs{}, err
		}
		if exists {
			seedRef := ref
			if requestedExists {
				seedRef = requestedRef
			}
			return worktreeRefs{seedRef: seedRef, baseRef: ref}, nil
		}
	}

	// Preserve the adapter's fallback when the configured default is missing:
	// an already-fetched session branch or same-named tag can still be resumed,
	// but is used as the comparison ref only because no default ref is available.
	if requestedExists {
		return worktreeRefs{seedRef: requestedRef, baseRef: requestedRef}, nil
	}
	// Also probe a same-named tag so requests like `--branch v1.2.3` can
	// auto-track when the tag is fetched but no branch ref exists.
	tagRef := "refs/tags/" + branch
	fallbackCandidates := []string{branch, tagRef}
	for _, ref := range fallbackCandidates {
		exists, err := w.refExists(ctx, repo, ref)
		if err != nil {
			return worktreeRefs{}, err
		}
		if exists {
			return worktreeRefs{seedRef: ref, baseRef: ref}, nil
		}
	}
	allCandidates := append([]string{requestedRef}, baseCandidates...)
	allCandidates = append(allCandidates, fallbackCandidates...)
	return worktreeRefs{}, fmt.Errorf("%w for branch %q (tried %s)", errNoBaseRef, branch, strings.Join(allCandidates, ", "))
}

func (w *Workspace) refExists(ctx context.Context, repo, ref string) (bool, error) {
	_, err := w.run(ctx, w.binary, revParseVerifyArgs(repo, ref)...)
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("gitworktree: verify ref %q: %w", ref, err)
}

func (w *Workspace) listRecords(ctx context.Context, repo string) ([]worktreeRecord, error) {
	out, err := w.run(ctx, w.binary, worktreeListPorcelainArgs(repo)...)
	if err != nil {
		return nil, fmt.Errorf("gitworktree: worktree list: %w", err)
	}
	records, err := parseWorktreePorcelain(string(out))
	if err != nil {
		return nil, fmt.Errorf("gitworktree: parse worktree list: %w", err)
	}
	return records, nil
}
