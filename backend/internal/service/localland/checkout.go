package localland

import (
	"context"
	"fmt"
	"strings"
)

// checkouts lists the worktrees of repo that have refs/heads/<branch> checked out.
func checkouts(ctx context.Context, repo, branch string) ([]string, error) {
	out, err := git(ctx, repo, "", "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var paths []string
	current := ""
	for _, line := range strings.Split(out, "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			current = p
		}
		if line == "branch refs/heads/"+branch && current != "" {
			paths = append(paths, current)
		}
	}
	return paths, nil
}

// checkClean refuses a checkout with staged or modified tracked files, or one
// whose untracked files the fast-forward from old to new would overwrite.
func checkClean(ctx context.Context, wt, old, updated string) error {
	status, err := git(ctx, wt, "", "status", "--porcelain=v1", "--untracked-files=no")
	if err != nil {
		return err
	}
	if status != "" {
		return fmt.Errorf("%w: %s has uncommitted changes to tracked files", ErrDirtyCheckout, wt)
	}
	if _, err := git(ctx, wt, "", "read-tree", "-m", "-u", "-n", old, updated); err != nil {
		return fmt.Errorf("%w: %s: fast-forward would overwrite files: %w", ErrDirtyCheckout, wt, err)
	}
	return nil
}

// moveLocal moves refs/heads/<branch> from old to updated by compare-and-swap
// and fast-forwards each checkout of it. Every checkout is checked first, so a
// refusal moves nothing.
func moveLocal(ctx context.Context, repo, branch, old, updated string, beforeUpdate func()) ([]string, error) {
	wts, err := checkouts(ctx, repo, branch)
	if err != nil {
		return nil, err
	}
	for _, wt := range wts {
		if err := checkClean(ctx, wt, old, updated); err != nil {
			return nil, err
		}
	}
	if beforeUpdate != nil {
		beforeUpdate()
	}
	if _, err := git(ctx, repo, "", "update-ref", "-m", "tallpine session land", "refs/heads/"+branch, updated, old); err != nil {
		return nil, fmt.Errorf("%w: %s is no longer at %s", ErrTargetMoved, branch, short(old))
	}
	for _, wt := range wts {
		if _, err := git(ctx, wt, "", "read-tree", "-m", "-u", old, updated); err != nil {
			return wts, fmt.Errorf("%s moved to %s but updating checkout %s failed: %w", branch, short(updated), wt, err)
		}
	}
	return wts, nil
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
