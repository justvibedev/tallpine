package importer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func normalizeImportPath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("path is required")
	}
	if raw == "~" || strings.HasPrefix(raw, "~/") || strings.HasPrefix(raw, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if raw == "~" {
			raw = home
		} else {
			raw = filepath.Join(home, raw[2:])
		}
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

// unsafeImportPath protects broad user and Tallpine-owned directories from the Git
// preparation actions below. Import preparation is deliberately separate from
// project setup, so it cannot rely on the latter's path-safety guard.
func unsafeImportPath(path string) bool {
	clean := comparableImportPath(path)
	if filepath.Dir(clean) == clean {
		return true
	}

	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return false
	}
	home = comparableImportPath(home)
	if sameImportPath(clean, home) {
		return true
	}
	for _, broadName := range []string{"Desktop", "Documents", "Downloads"} {
		if sameImportPath(clean, comparableImportPath(filepath.Join(home, broadName))) {
			return true
		}
	}
	tallpineState := comparableImportPath(filepath.Join(home, brand.HomeDirName))
	return sameImportPath(clean, tallpineState) || isImportDescendant(clean, tallpineState)
}

func isImportDescendant(path, parent string) bool {
	rel, err := filepath.Rel(parent, path)
	if err != nil || rel == "." || rel == "" || rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func isImportFolderEmpty(path string) bool {
	entries, err := os.ReadDir(path)
	return err == nil && len(entries) == 0
}

func isImportGitRepo(path string) bool {
	out, err := ports.DefaultGit().TopLevel(context.Background(), path)
	if err != nil {
		return false
	}
	top := normalizeImportGitPath(path, out)
	return sameImportPath(top, comparableImportPath(path))
}

func isBareImportRepo(ctx context.Context, path string) bool {
	out, err := importGitOutput(ctx, path, "rev-parse", "--is-bare-repository")
	return err == nil && strings.TrimSpace(out) == "true"
}

func importRepoHasCommit(ctx context.Context, path string) bool {
	_, err := importGitOutput(ctx, path, "rev-parse", "--verify", "HEAD")
	return err == nil
}

func importRepoHasDetachedHead(ctx context.Context, path string) bool {
	_, err := importGitOutput(ctx, path, "symbolic-ref", "--quiet", "--short", "HEAD")
	return err != nil
}

func importRemoteExists(path, name string) bool {
	out, err := ports.DefaultGit().Output(context.Background(), path, "remote")
	if err != nil {
		return false
	}
	return slices.Contains(strings.Fields(out), name)
}

func resolveImportOriginURL(path string) string {
	// `git remote get-url origin` falls back to the literal string "origin"
	// when the remote section exists without a URL. Read the configured value
	// directly so validation can repair that incomplete state.
	out, err := ports.DefaultGit().RemoteURL(context.Background(), path, "origin")
	if err != nil {
		return ""
	}
	return out
}

func hasUnsupportedImportGitMetadata(path string) bool {
	if isImportGitRepo(path) {
		return false
	}
	_, err := os.Lstat(filepath.Join(path, ".git"))
	return err == nil || !errors.Is(err, os.ErrNotExist)
}

func normalizeImportGitPath(base, reported string) string {
	if reported == "" {
		return comparableImportPath(reported)
	}
	if !filepath.IsAbs(reported) {
		reported = filepath.Join(base, reported)
	}
	return comparableImportPath(reported)
}

func comparableImportPath(path string) string {
	clean := filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(clean); err == nil {
		clean = resolved
	}
	return filepath.Clean(clean)
}

func sameImportPath(a, b string) bool {
	return strings.EqualFold(a, b) || a == b
}

func actionSet(actions []string) map[string]bool {
	out := make(map[string]bool, len(actions))
	for _, action := range actions {
		out[action] = true
	}
	return out
}

func containsAction(actions []string, want string) bool {
	for _, action := range actions {
		if action == want {
			return true
		}
	}
	return false
}

func importGitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := ports.DefaultGit().Run(ctx, dir, args...)
	if err != nil {
		return "", fmt.Errorf("git -C %s %s: %w", dir, strings.Join(args, " "), err)
	}
	return out, nil
}
