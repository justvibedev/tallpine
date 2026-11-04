package migratevo

import (
	"context"
	"database/sql"
	"path/filepath"
)

// addChildWorktrees appends the per-repo worktrees of workspace sessions: each
// lives at session_worktrees.worktree_path and belongs to the child repo at
// <project.path>/<workspace_repos.relative_path>.
func (e *Env) addChildWorktrees(ctx context.Context, db *sql.DB, wts []WorktreePlan) ([]WorktreePlan, error) {
	ok, err := columnExists(ctx, db, ColumnRef{"session_worktrees", "worktree_path"})
	if err != nil || !ok {
		return wts, err
	}
	rows, err := db.QueryContext(ctx, `
SELECT p.path, r.relative_path, w.worktree_path
FROM session_worktrees w
JOIN sessions s ON s.id = w.session_id
JOIN projects p ON p.id = s.project_id
JOIN workspace_repos r ON r.project_id = p.id AND r.name = w.repo_name
WHERE w.worktree_path <> '' ORDER BY w.session_id, w.repo_name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	have := map[string]bool{}
	for _, w := range wts {
		have[w.Old] = true
	}
	for rows.Next() {
		var proj, rel, wt string
		if err := rows.Scan(&proj, &rel, &wt); err != nil {
			return nil, err
		}
		newWT, moved := e.MapPath(wt)
		if !moved || have[wt] {
			continue
		}
		repo, _ := e.MapPath(filepath.Join(proj, filepath.FromSlash(rel)))
		wts = append(wts, WorktreePlan{Repo: repo, Old: wt, New: newWT})
		have[wt] = true
	}
	return wts, rows.Err()
}
