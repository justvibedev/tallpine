package migratevo

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// RepairWorktrees runs `git worktree repair` once per main repo for every
// worktree that exists at its new path, then verifies each with
// `git rev-parse --git-dir`. Worktrees whose directory is gone (cleaned-up
// sessions) are skipped. Returns the number repaired and any failures.
func (e *Env) RepairWorktrees(ctx context.Context, wts []WorktreePlan) (repaired int, failures []string) {
	byRepo := map[string][]string{}
	for _, w := range wts {
		if _, err := os.Lstat(filepath.Join(w.New, ".git")); err != nil {
			continue // not a git worktree on disk (cleaned up, or a plain dir)
		}
		byRepo[w.Repo] = append(byRepo[w.Repo], w.New)
	}
	repos := make([]string, 0, len(byRepo))
	for r := range byRepo {
		repos = append(repos, r)
	}
	sort.Strings(repos)
	for _, repo := range repos {
		paths := byRepo[repo]
		if _, err := e.Run(ctx, repo, "git", append([]string{"worktree", "repair"}, paths...)...); err != nil {
			failures = append(failures, fmt.Sprintf("repair %s: %v", repo, err))
			continue
		}
		for _, p := range paths {
			if _, err := e.Run(ctx, p, "git", "rev-parse", "--git-dir"); err != nil {
				failures = append(failures, fmt.Sprintf("verify %s: %v", p, err))
				continue
			}
			repaired++
		}
	}
	return repaired, failures
}
