package project

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/gitdefault"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// gitOriginTimeout bounds the origin probe so a hung mount or slow git cannot
// block the caller.
const gitOriginTimeout = 5 * time.Second

// resolveGitOriginURL returns the project's `origin` remote URL via
// `git -C path remote get-url origin`. A missing remote, missing repo, or any
// other git error returns an empty string — `project add` must not fail just
// because no origin is configured (the SCM observer skips such projects).
func resolveGitOriginURL(path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), gitOriginTimeout)
	defer cancel()
	out, err := ports.DefaultGit().Output(ctx, path, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// resolveDefaultBranch inspects only authoritative local metadata: a cached
// remote HEAD, or the branch Tallpine recorded when it initialized a remoteless
// repo. It never guesses main/master; a remoteless repo with nothing recorded
// stays "auto" here and only the workspace spawn path falls back to local HEAD. Live remote lookup stays on the bounded workspace spawn path.
func resolveDefaultBranch(ctx context.Context, path string) string {
	resolution, err := gitdefault.New("git", nil).Inspect(ctx, path)
	if err != nil {
		return ""
	}
	return resolution.Branch
}

func normalizePath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", apierr.Invalid("PATH_REQUIRED", "Repository path is required", nil)
	}
	if strings.HasPrefix(raw, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", apierr.Invalid("INVALID_PATH", "Repository path could not be expanded", nil)
		}
		if raw == "~" {
			raw = home
		} else if strings.HasPrefix(raw, "~/") || strings.HasPrefix(raw, `~\`) {
			raw = filepath.Join(home, raw[2:])
		}
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", apierr.Invalid("INVALID_PATH", "Repository path is invalid", nil)
	}
	return filepath.Clean(abs), nil
}

// folderExists reports whether path currently exists as a directory. Only
// os.ErrNotExist (or a path that is not a directory) means "missing". All other
// stat errors are surfaced so the caller can log them and avoid guessing.
func folderExists(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if !info.IsDir() {
		return false, nil
	}
	return true, nil
}

func ensureDirectoryPath(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return apierr.Invalid("INVALID_PATH", "Selected folder could not be read", map[string]any{"path": path})
	}
	if !info.IsDir() {
		return apierr.Invalid("INVALID_PATH", "Selected path must be a folder", map[string]any{"path": path})
	}
	return nil
}
