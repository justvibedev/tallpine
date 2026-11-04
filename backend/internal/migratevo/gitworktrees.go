package migratevo

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// addGitListedWorktrees appends worktrees that live under the source root but
// belong to no session row (release-builds/<sha>, dev/**): each registered
// repo's `git worktree list --porcelain` names them. They are repaired like
// session worktrees, or git keeps them as prunable after the rename.
func (e *Env) addGitListedWorktrees(ctx context.Context, db *sql.DB, wts []WorktreePlan) ([]WorktreePlan, error) {
	repos, err := e.registeredRepos(ctx, db)
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, w := range wts {
		have[w.Old] = true
	}
	for _, repo := range repos {
		if _, err := os.Stat(repo); err != nil {
			continue
		}
		out, err := e.Run(ctx, repo, "git", "worktree", "list", "--porcelain")
		if err != nil {
			continue // not a git repo (anymore); nothing to repair
		}
		repoNew, _ := e.MapPath(repo)
		for _, old := range underSource(parseWorktreeList(string(out)), e.Source) {
			if have[old] || old == repo {
				continue
			}
			newPath, _ := e.MapPath(old)
			wts = append(wts, WorktreePlan{Repo: repoNew, Old: old, New: newPath})
			have[old] = true
		}
	}
	return wts, nil
}

// registeredRepos returns the sorted, de-duplicated main repo paths: every
// project and every workspace child repo.
func (e *Env) registeredRepos(ctx context.Context, db *sql.DB) ([]string, error) {
	set := map[string]bool{}
	collect := func(q string, join bool) error {
		rows, err := db.QueryContext(ctx, q)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var p, rel string
			if join {
				err = rows.Scan(&p, &rel)
				p = filepath.Join(p, filepath.FromSlash(rel))
			} else {
				err = rows.Scan(&p)
			}
			if err != nil {
				return err
			}
			if p != "" {
				set[p] = true
			}
		}
		return rows.Err()
	}
	if err := collect(`SELECT path FROM projects`, false); err != nil {
		return nil, err
	}
	if ok, err := columnExists(ctx, db, ColumnRef{"workspace_repos", "relative_path"}); err == nil && ok {
		if err := collect(`SELECT p.path, r.relative_path FROM workspace_repos r JOIN projects p ON p.id = r.project_id`, true); err != nil {
			return nil, err
		}
	}
	repos := make([]string, 0, len(set))
	for r := range set {
		repos = append(repos, r)
	}
	sort.Strings(repos)
	return repos, nil
}

// parseWorktreeList returns the paths from `git worktree list --porcelain`.
func parseWorktreeList(out string) []string {
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		if p, ok := strings.CutPrefix(strings.TrimRight(line, "\r"), "worktree "); ok && p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

// underSource keeps the paths strictly inside the source root.
func underSource(paths []string, source string) []string {
	var keep []string
	for _, p := range paths {
		if strings.HasPrefix(p, source+string(filepath.Separator)) {
			keep = append(keep, p)
		}
	}
	return keep
}
