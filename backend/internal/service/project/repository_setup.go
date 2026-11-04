package project

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/gitdefault"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type repositorySetupTarget int

const (
	repositorySetupPlainFolder repositorySetupTarget = iota
	repositorySetupUnbornRepo
)

// InitializeRepository prepares a selected folder for project registration by ensuring it has an initial Git commit.
func (m *Service) InitializeRepository(ctx context.Context, in InitializeRepositoryInput) (result InitializeRepositoryResult, retErr error) {
	path, err := normalizePath(in.Path)
	if err != nil {
		return InitializeRepositoryResult{}, err
	}
	if err := ensureDirectoryPath(path); err != nil {
		return InitializeRepositoryResult{}, err
	}
	if err := validateRepositorySetupPathSafety(path); err != nil {
		return InitializeRepositoryResult{}, err
	}

	m.addMu.Lock()
	defer m.addMu.Unlock()

	target, err := classifyRepositorySetupTarget(ctx, path)
	if err != nil {
		return InitializeRepositoryResult{}, err
	}

	if err := rejectNestedGitRepositories(path); err != nil {
		return InitializeRepositoryResult{}, err
	}

	if target == repositorySetupPlainFolder {
		rollback := snapshotPlainFolderRepositorySetup(path)
		defer func() {
			if retErr != nil {
				rollback()
			}
		}()
		if _, err := gitOutput(ctx, path, "init", "-b", domain.DefaultBranchName); err != nil {
			return InitializeRepositoryResult{}, apierr.Invalid("GIT_INIT_FAILED", "Could not initialize a Git repository in this folder.", map[string]any{"error": err.Error()})
		}
	}
	if err := gitdefault.New("git", nil).RecordInitialBranch(ctx, path); err != nil {
		return InitializeRepositoryResult{}, apierr.Invalid("GIT_INIT_FAILED", "Could not record the default branch for this repository.", map[string]any{"error": err.Error()})
	}

	if _, err := gitOutput(ctx, path, "add", "-A"); err != nil {
		return InitializeRepositoryResult{}, apierr.Invalid("GIT_ADD_FAILED", "Could not stage files for the initial commit.", map[string]any{"error": err.Error()})
	}
	if _, err := gitOutput(ctx, path, "-c", "user.name=Tallpine", "-c", "user.email=tallpine@example.com", "commit", "--allow-empty", "-m", "initial commit"); err != nil {
		return InitializeRepositoryResult{}, apierr.Invalid("INITIAL_COMMIT_FAILED", "Could not create the initial commit.", map[string]any{"error": err.Error()})
	}
	return InitializeRepositoryResult{Path: path}, nil
}

func classifyRepositorySetupTarget(ctx context.Context, path string) (repositorySetupTarget, error) {
	if isBareGitRepository(ctx, path) {
		return repositorySetupPlainFolder, apierr.Invalid("PROJECT_BARE_REPOSITORY", "Selected folder must be a non-bare Git repository or a plain folder.", map[string]any{
			"path":         path,
			"suggestedFix": "Use a normal checkout, or select a plain folder for Tallpine to initialize.",
		})
	}

	if isGitRepo(path) {
		if repoHasCommit(ctx, path) {
			return repositorySetupUnbornRepo, apierr.Conflict("PROJECT_ALREADY_INITIALIZED", "This repository already has commits.", map[string]any{"path": path})
		}
		return repositorySetupUnbornRepo, nil
	}

	if hasGitMetadata(path) {
		return repositorySetupPlainFolder, apierr.Invalid("UNSUPPORTED_GIT_REPO", "Selected folder contains Git metadata that Tallpine could not inspect.", map[string]any{
			"path":         path,
			"suggestedFix": "Repair the Git repository or select a plain folder.",
		})
	}

	return repositorySetupPlainFolder, nil
}

func validateRepositorySetupPathSafety(path string) error {
	clean := comparablePath(path)
	if isFilesystemRoot(clean) {
		return unsafeRepositorySetupPathError(path, "filesystem root")
	}

	home, _ := os.UserHomeDir()
	if strings.TrimSpace(home) == "" {
		return nil
	}
	home = comparablePath(home)
	if samePath(clean, home) {
		return unsafeRepositorySetupPathError(path, "home directory")
	}

	for _, broadName := range []string{"Desktop", "Documents", "Downloads"} {
		if samePath(clean, comparablePath(filepath.Join(home, broadName))) {
			return unsafeRepositorySetupPathError(path, strings.ToLower(broadName)+" directory")
		}
	}

	tallpineState := comparablePath(filepath.Join(home, brand.HomeDirName))
	if samePath(clean, tallpineState) || isDescendantPath(clean, tallpineState) {
		return unsafeRepositorySetupPathError(path, "Tallpine state directory")
	}
	return nil
}

func unsafeRepositorySetupPathError(path, reason string) error {
	return apierr.Invalid("PROJECT_SETUP_PATH_UNSAFE", "Selected folder is too broad for automatic Git setup.", map[string]any{
		"path":         path,
		"reason":       reason,
		"suggestedFix": "Select a specific project folder instead.",
	})
}

func isFilesystemRoot(path string) bool {
	clean := filepath.Clean(path)
	return filepath.Dir(clean) == clean
}

func isDescendantPath(path, parent string) bool {
	rel, err := filepath.Rel(parent, path)
	if err != nil || rel == "." || rel == "" || rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func snapshotPlainFolderRepositorySetup(path string) func() {
	gitignorePath := filepath.Join(path, ".gitignore")
	originalGitignore, readErr := os.ReadFile(gitignorePath)
	gitignoreExisted := readErr == nil
	gitignoreMissing := errors.Is(readErr, os.ErrNotExist)
	gitignoreMode := fs.FileMode(0o600)
	if gitignoreExisted {
		if info, err := os.Stat(gitignorePath); err == nil {
			gitignoreMode = info.Mode().Perm()
		}
	}

	return func() {
		_ = os.RemoveAll(filepath.Join(path, ".git"))
		if gitignoreExisted {
			_ = os.WriteFile(gitignorePath, originalGitignore, gitignoreMode) // #nosec G703 -- path is the user-registered project root; writing its .gitignore is the intended action
		} else if gitignoreMissing {
			_ = os.Remove(gitignorePath)
		}
	}
}

func rejectNestedGitRepositories(path string) error {
	nested, err := nestedGitRepositoryPaths(path)
	if err != nil {
		return apierr.Invalid("PROJECT_NESTED_REPO_SCAN_FAILED", "Selected folder could not be inspected for nested Git repositories.", map[string]any{
			"path":  path,
			"error": err.Error(),
		})
	}
	if len(nested) == 0 {
		return nil
	}
	return apierr.Invalid("PROJECT_NESTED_GIT_REPOSITORY", "Selected folder contains nested Git repositories. Select the project repository directly or import the parent folder as a workspace.", map[string]any{
		"path":               path,
		"nestedRepositories": nested,
		"suggestedFix":       "Select one nested repository directly, or import the parent folder as a workspace.",
	})
}

func nestedGitRepositoryPaths(root string) ([]string, error) {
	root = filepath.Clean(root)
	rootGitPath := filepath.Join(root, ".git")
	var nested []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root || entry.Name() != ".git" {
			return nil
		}

		clean := filepath.Clean(path)
		if samePath(comparablePath(clean), comparablePath(rootGitPath)) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		repoPath := filepath.Dir(clean)
		rel, err := filepath.Rel(root, repoPath)
		if err != nil {
			rel = repoPath
		}
		nested = append(nested, filepath.ToSlash(rel))
		if entry.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return nested, nil
}

func repoHasCommit(ctx context.Context, path string) bool {
	_, err := gitOutput(ctx, path, "rev-parse", "--verify", "HEAD")
	return err == nil
}

func isBareGitRepository(ctx context.Context, path string) bool {
	out, err := gitOutput(ctx, path, "rev-parse", "--is-bare-repository")
	return err == nil && strings.TrimSpace(out) == "true"
}

func hasGitMetadata(path string) bool {
	_, err := os.Lstat(filepath.Join(path, ".git"))
	return err == nil || !errors.Is(err, os.ErrNotExist)
}

func normalizeGitReportedPath(base, reported string) string {
	if reported == "" {
		return comparablePath(reported)
	}
	if !filepath.IsAbs(reported) {
		reported = filepath.Join(base, reported)
	}
	return comparablePath(reported)
}

func comparablePath(path string) string {
	clean := filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(clean); err == nil {
		clean = resolved
	}
	return filepath.Clean(clean)
}

func samePath(a, b string) bool {
	if strings.EqualFold(a, b) {
		return true
	}
	return a == b
}

func isGitRepo(path string) bool {
	out, err := ports.DefaultGit().TopLevel(context.Background(), path)
	if err != nil {
		return false
	}
	top := normalizeGitReportedPath(path, out)
	return samePath(top, comparablePath(path))
}
