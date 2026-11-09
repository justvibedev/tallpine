package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/pathx"
	"tallpine.eu/tallpine/backend/internal/ports"
	tallpineprocess "tallpine.eu/tallpine/backend/internal/process"
	"tallpine.eu/tallpine/backend/internal/textutil"
)

func (w *Workspace) repoPath(project domain.ProjectID) (string, error) {
	repo, err := w.repos.RepoPath(project)
	if err != nil {
		return "", err
	}
	if repo == "" {
		return "", fmt.Errorf("gitworktree: no repo configured for project %q", project)
	}
	abs, err := physicalAbs(repo)
	if err != nil {
		return "", fmt.Errorf("gitworktree: repo path: %w", err)
	}
	return abs, nil
}

func (w *Workspace) repoPathForInfo(info ports.WorkspaceInfo) (string, error) {
	if info.RepoPath != "" {
		repo, err := physicalAbs(info.RepoPath)
		if err != nil {
			return "", fmt.Errorf("gitworktree: repo path: %w", err)
		}
		return repo, nil
	}
	if info.ProjectID == "" {
		return "", errors.New("gitworktree: project id is required")
	}
	return w.repoPath(info.ProjectID)
}

// requireReachableRepo reports the project repo as unavailable when it is no
// longer on disk. Teardown is the only caller: every git command it runs is
// `git -C <repo> ...`, so a deleted project directory turns each one into an
// opaque exit 128, and the session it belongs to can never be deleted. Create
// and Restore deliberately do not use this: there, a missing repo must fail.
func (w *Workspace) requireReachableRepo(repo string) error {
	if info, err := os.Stat(repo); err == nil && info.IsDir() {
		return nil
	}
	return fmt.Errorf("gitworktree: repository %q is no longer on disk: %w", repo, ports.ErrWorkspaceRepoUnavailable)
}

func (w *Workspace) repoPathForConfig(cfg ports.WorkspaceConfig) (string, error) {
	if cfg.RepoPath != "" {
		repo, err := physicalAbs(cfg.RepoPath)
		if err != nil {
			return "", fmt.Errorf("gitworktree: repo path: %w", err)
		}
		return repo, nil
	}
	return w.repoPath(cfg.ProjectID)
}

// enableWorktreeConfig opts linked worktrees into Git's per-worktree config
// file. Without this extension, every worktree uses the shared .git/config,
// so a worker's `git config` write can alter the human checkout as well. Git's
// remote commands, plain `git config` and `git config --local` remain
// repository-scoped even with this extension, so each session's git shim
// (agentlaunch.EnsureGitShim) refuses them and points agents at
// `git config --worktree`.
//
// Repositories registered with Tallpine are standalone checkouts. The .git check
// avoids turning unit-test fakes and non-git paths into a new error before the
// normal worktree commands report their more useful failure.
func (w *Workspace) enableWorktreeConfig(ctx context.Context, repo string) error {
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("gitworktree: inspect repository metadata: %w", err)
	}
	// Reading first keeps the common case (already enabled) from writing the
	// shared config at all. --local: git honors extensions only from the
	// repository's own config, so a global or system value must not skip it.
	if out, err := tallpineprocess.CommandContext(ctx, w.binary, "-C", repo, "config", "--local", "--type=bool", "--get", "extensions.worktreeConfig").Output(); err == nil && strings.TrimSpace(string(out)) == "true" {
		return nil
	}
	unlock, err := repoConfigLock(repo)
	if err != nil {
		return err
	}
	defer unlock()
	args := []string{"-C", repo, "config", "extensions.worktreeConfig", "true"}
	cmd := tallpineprocess.CommandContext(ctx, w.binary, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("gitworktree: enable per-worktree config: %w", commandError{
			args: append([]string{w.binary}, args...), output: string(out), err: err,
		})
	}
	return nil
}

func physicalAbs(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(resolved), nil
	}
	parent := filepath.Dir(abs)
	base := filepath.Base(abs)
	for parent != "." && parent != string(os.PathSeparator) {
		if resolved, err := filepath.EvalSymlinks(parent); err == nil {
			return filepath.Join(resolved, base), nil
		}
		base = filepath.Join(filepath.Base(parent), base)
		parent = filepath.Dir(parent)
	}
	if resolved, err := filepath.EvalSymlinks(parent); err == nil {
		return filepath.Join(resolved, base), nil
	}
	return abs, nil
}

func validateConfig(cfg ports.WorkspaceConfig) error {
	if cfg.ProjectID == "" {
		return errors.New("gitworktree: project id is required")
	}
	if err := validatePathComponent("project id", string(cfg.ProjectID)); err != nil {
		return err
	}
	if cfg.Kind == domain.KindOrchestrator {
		prefix := resolvedSessionPrefix(cfg)
		if err := validatePathComponent("session prefix", prefix); err != nil {
			return err
		}
	} else {
		if cfg.SessionID == "" {
			return errors.New("gitworktree: session id is required")
		}
		if err := validatePathComponent("session id", string(cfg.SessionID)); err != nil {
			return err
		}
	}
	if cfg.Branch == "" {
		return errors.New("gitworktree: branch is required")
	}
	return nil
}

func validateWorkspaceProjectConfig(cfg ports.WorkspaceProjectConfig) error {
	if err := validateConfig(ports.WorkspaceConfig{
		ProjectID:     cfg.ProjectID,
		SessionID:     cfg.SessionID,
		Kind:          cfg.Kind,
		SessionPrefix: cfg.SessionPrefix,
		Branch:        textutil.FirstTrimmed(cfg.Branch, defaultSessionBranchName(cfg.SessionID)),
		BaseBranch:    cfg.BaseBranch,
	}); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.RootRepoPath) == "" {
		return errors.New("gitworktree: root repo path is required")
	}
	for _, repo := range cfg.Repos {
		if strings.TrimSpace(repo.Name) == "" {
			return errors.New("gitworktree: child repo name is required")
		}
		if err := validatePathComponent("child repo name", repo.Name); err != nil {
			return err
		}
		if strings.TrimSpace(repo.RepoPath) == "" {
			return fmt.Errorf("gitworktree: child repo %q path is required", repo.Name)
		}
		if _, err := cleanRelativePath(repo.RelativePath); err != nil {
			return fmt.Errorf("gitworktree: child repo %q: %w", repo.Name, err)
		}
	}
	return nil
}

// validatePathComponent rejects id values that could escape the managed root
// once joined into a path. filepath.Join cleans `..` before validateManagedPath
// runs, so a session id of "../other" would otherwise resolve back inside
// managedRoot while breaking per-project isolation. Reject any path separator
// or the special `.`/`..` components at the source.
func validatePathComponent(name, value string) error {
	if strings.ContainsAny(value, `/\`) {
		return fmt.Errorf("%w: %s %q must not contain path separators", ErrUnsafePath, name, value)
	}
	if value == "." || value == ".." {
		return fmt.Errorf("%w: %s %q must not be a path-traversal component", ErrUnsafePath, name, value)
	}
	return nil
}

func (w *Workspace) managedPath(cfg ports.WorkspaceConfig) (string, error) {
	var path string
	if cfg.Kind == domain.KindOrchestrator {
		prefix := resolvedSessionPrefix(cfg)
		path = filepath.Join(w.managedRoot, string(cfg.ProjectID), "orchestrator", prefix+"-orchestrator")
	} else {
		path = filepath.Join(w.managedRoot, string(cfg.ProjectID), string(cfg.SessionID))
	}
	return w.validateManagedPath(path)
}

func (w *Workspace) restorePath(cfg ports.WorkspaceConfig) (string, error) {
	if cfg.Path != "" {
		return w.validateManagedPath(cfg.Path)
	}
	return w.managedPath(cfg)
}

// resolvedSessionPrefix returns cfg.SessionPrefix when set, otherwise the first
// 12 characters of the project ID (matching the display-prefix convention).
func resolvedSessionPrefix(cfg ports.WorkspaceConfig) string {
	if p := strings.TrimSpace(cfg.SessionPrefix); p != "" {
		return p
	}
	id := string(cfg.ProjectID)
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}

func defaultSessionBranchName(id domain.SessionID) string {
	return brand.BranchPrefix + "/" + string(id)
}

func cleanRelativePath(path string) (string, error) {
	rel := filepath.ToSlash(strings.TrimSpace(path))
	if rel == "" {
		return "", errors.New("relative path is required")
	}
	if strings.HasPrefix(rel, "/") {
		return "", fmt.Errorf("%w: relative path %q must not be absolute", ErrUnsafePath, path)
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(rel)))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("%w: relative path %q escapes the workspace root", ErrUnsafePath, path)
	}
	return clean, nil
}

func (w *Workspace) validateManagedPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("%w: empty path", ErrUnsafePath)
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%w: %q is not absolute", ErrUnsafePath, path)
	}
	clean := filepath.Clean(path)
	if clean != path {
		return "", fmt.Errorf("%w: %q is not clean", ErrUnsafePath, path)
	}
	physical, err := physicalAbs(clean)
	if err != nil {
		return "", fmt.Errorf("gitworktree: resolve path %q: %w", path, err)
	}
	clean = physical
	if !pathx.StrictlyWithin(w.managedRoot, clean) {
		return "", fmt.Errorf("%w: %q is outside managed root %q", ErrUnsafePath, clean, w.managedRoot)
	}
	return clean, nil
}

func findWorktree(records []worktreeRecord, path string) (worktreeRecord, bool) {
	clean := filepath.Clean(path)
	for _, rec := range records {
		if filepath.Clean(rec.Path) == clean {
			return rec, true
		}
	}
	return worktreeRecord{}, false
}

func findWorktreeByBranch(records []worktreeRecord, branch string) (worktreeRecord, bool) {
	for _, rec := range records {
		if rec.Branch == branch {
			return rec, true
		}
	}
	return worktreeRecord{}, false
}

func pathExistsNonEmpty(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if err == nil {
		return len(entries) > 0, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("gitworktree: inspect path %q: %w", path, err)
}

func moveStrayPathAside(path string) (string, error) {
	for i := 0; i < 100; i++ {
		candidate := path + ".stray"
		if i > 0 {
			candidate = fmt.Sprintf("%s.stray-%d", path, i+1)
		}
		if _, err := os.Lstat(candidate); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("gitworktree: inspect stray destination %q: %w", candidate, err)
		}
		if err := os.Rename(path, candidate); err != nil {
			return "", fmt.Errorf("gitworktree: move stray path %q aside to %q: %w", path, candidate, err)
		}
		return candidate, nil
	}
	return "", fmt.Errorf("gitworktree: move stray path %q aside: no available destination", path)
}
