package migratevo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Rollback undoes the whole-tree rename: it requires the old root to hold
// nothing (or only an old README stub), moves the new root back, then repairs every
// planned worktree to its old path. The caller (release-local) restores the DB
// snapshot afterwards; the plan file is kept as <plan>.rolledback.
func (e *Env) Rollback(ctx context.Context) error {
	planPath := e.FindPlanFile()
	plan, err := e.LoadPlan()
	if err != nil {
		return err
	}
	if plan == nil {
		return errors.New("no migration plan found; nothing to roll back")
	}
	if _, err := os.Lstat(e.Target); err != nil {
		return fmt.Errorf("new home %s not found; nothing to roll back: %w", e.Target, err)
	}
	if entries, err := os.ReadDir(e.Source); err == nil {
		for _, ent := range entries {
			if ent.Name() != readmeName {
				return fmt.Errorf("%s holds %q besides the README; refusing to overwrite it", e.Source, ent.Name())
			}
		}
		// Exactly the stub the migrator wrote: one known file, then the empty dir.
		if err := os.Remove(filepath.Join(e.Source, readmeName)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Remove(e.Source); err != nil {
			return err
		}
	}
	if err := os.Rename(e.Target, e.Source); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", e.Target, e.Source, err)
	}
	e.printf("moved %s back to %s\n", e.Target, e.Source)
	var failures []string
	for _, w := range plan.Worktrees {
		if _, err := os.Lstat(filepath.Join(w.Old, ".git")); err != nil {
			continue
		}
		repo := w.Repo
		if rest, ok := strings.CutPrefix(repo, e.Target+string(filepath.Separator)); ok {
			repo = filepath.Join(e.Source, rest)
		}
		if _, err := e.Run(ctx, repo, "git", "worktree", "repair", w.Old); err != nil {
			failures = append(failures, fmt.Sprintf("repair %s: %v", w.Old, err))
			continue
		}
		if _, err := e.Run(ctx, w.Old, "git", "rev-parse", "--git-dir"); err != nil {
			failures = append(failures, fmt.Sprintf("verify %s: %v", w.Old, err))
		}
	}
	// The plan travels with the tree: it was under the new root, now under the old.
	if moved, ok := e.MapPathBack(planPath); ok {
		planPath = moved
	}
	if err := os.Rename(planPath, planPath+".rolledback"); err != nil {
		return err
	}
	if len(failures) > 0 {
		return fmt.Errorf("%d worktree failures, first: %s", len(failures), failures[0])
	}
	return nil
}
