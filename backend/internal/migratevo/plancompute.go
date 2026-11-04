package migratevo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// openForPlan opens the source DB without modifying anything under the source
// root. With no pending WAL the file is opened immutable; otherwise db+wal are
// cloned into a temp dir first (a read-only open of a WAL DB may create -shm).
func (e *Env) openForPlan(ctx context.Context) (*sql.DB, func(), error) {
	src := e.SourceDB()
	noop := func() {}
	if _, err := os.Stat(src); err != nil {
		return nil, noop, err
	}
	path, extra, cleanup := src, "&mode=ro&immutable=1", noop
	if fi, err := os.Stat(src + "-wal"); err == nil && fi.Size() > 0 {
		tmp, err := os.MkdirTemp("", "migratevo-plan-")
		if err != nil {
			return nil, noop, err
		}
		cleanup = func() { _ = os.RemoveAll(tmp) }
		path, extra = filepath.Join(tmp, dbFileName), "&mode=ro"
		for _, suffix := range []string{"", "-wal"} {
			if _, err := e.Run(ctx, "", "cp", "-c", src+suffix, path+suffix); err != nil {
				if _, err2 := e.Run(ctx, "", "cp", src+suffix, path+suffix); err2 != nil {
					cleanup()
					return nil, noop, err
				}
			}
		}
	}
	db, err := sql.Open("sqlite", dsn(path, extra))
	if err != nil {
		cleanup()
		return nil, noop, err
	}
	return db, func() { _ = db.Close(); cleanup() }, nil
}

// ComputePlan inspects the source install and returns what a migration would
// do. It never writes under the source or target roots.
func (e *Env) ComputePlan(ctx context.Context, copyHarnessHistory bool) (*Plan, error) {
	db, closeDB, err := e.openForPlan(ctx)
	if err != nil {
		return nil, fmt.Errorf("open source db: %w", err)
	}
	defer closeDB()
	p := &Plan{Version: planVersion, Source: e.Source, Target: e.Target,
		CopyHarnessHistory: copyHarnessHistory, Steps: map[string]bool{}}
	if p.DBUpdates, err = CountPathRows(ctx, db, e.Source); err != nil {
		return nil, err
	}
	if p.Accounts, err = e.listAccounts(ctx, db); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `
SELECT s.id, s.harness, s.is_terminated, s.workspace_path,
       CASE WHEN s.workspace_repo_path <> '' THEN s.workspace_repo_path ELSE COALESCE(p.path, '') END
FROM sessions s LEFT JOIN projects p ON p.id = s.project_id
WHERE s.workspace_path <> '' ORDER BY s.id`)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, harness, ws, repo string
		var terminated bool
		if err := rows.Scan(&id, &harness, &terminated, &ws, &repo); err != nil {
			return nil, err
		}
		newWS, moved := e.MapPath(ws)
		if !moved {
			continue
		}
		if repo != "" {
			repoNew, _ := e.MapPath(repo)
			if repoNew != newWS {
				p.Worktrees = append(p.Worktrees, WorktreePlan{Repo: repoNew, Old: ws, New: newWS})
			}
		}
		if copyHarnessHistory && !terminated && harness == "claude-code" {
			cfgs, err := e.claudeConfigDirs(ctx, db, id)
			if err != nil {
				return nil, err
			}
			for _, cfg := range cfgs {
				p.ClaudeCopies = append(p.ClaudeCopies, e.planClaudeCopy(id, ws, newWS, cfg))
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if p.Worktrees, err = e.addChildWorktrees(ctx, db, p.Worktrees); err != nil {
		return nil, err
	}
	if p.Worktrees, err = e.addGitListedWorktrees(ctx, db, p.Worktrees); err != nil {
		return nil, err
	}
	return p, nil
}

// planClaudeCopy plans the clone of <cfg>/projects/<esc(old cwd)> to
// <cfg>/projects/<esc(new cwd)>. cfg is the Claude config dir as stored (before
// the rename); a config dir under the source root is recorded at its
// post-rename location because the copy runs after the rename.
func (e *Env) planClaudeCopy(id, oldCwd, newCwd, cfg string) ClaudeCopy {
	c := ClaudeCopy{Session: id}
	oldName, ok1 := ClaudeProjectDirName(oldCwd)
	newName, ok2 := ClaudeProjectDirName(newCwd)
	if !ok1 || !ok2 {
		c.Skip = "path too long to reproduce Claude's directory name"
		return c
	}
	projects := e.ClaudeProjects
	if cfg != "" {
		projects = filepath.Join(cfg, "projects")
	}
	preSrc := filepath.Join(projects, oldName)
	preDst := filepath.Join(projects, newName)
	mapped, _ := e.MapPath(projects)
	c.Src = filepath.Join(mapped, oldName)
	c.Dst = filepath.Join(mapped, newName)
	fi, err := os.Stat(preSrc)
	switch {
	case errors.Is(err, os.ErrNotExist):
		c.Skip = "no Claude project dir for this session"
	case err != nil:
		c.Skip = err.Error()
	case !fi.IsDir():
		c.Skip = "source is not a directory"
	default:
		if _, err := os.Lstat(preDst); err == nil {
			c.Skip = "target exists"
		}
		c.Bytes = dirSize(preSrc)
	}
	return c
}

func dirSize(root string) int64 {
	var n int64
	_ = filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}
