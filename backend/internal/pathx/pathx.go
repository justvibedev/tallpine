// Package pathx holds the one path-containment and home-expansion rule the
// backend shares. Containment is decided from filepath.Rel, never from a string
// prefix, so /a/bc is not inside /a/b.
package pathx

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Within reports whether p is root itself or lies inside it. The check is
// lexical: it follows no symlinks. Use WithinResolved when links matter.
func Within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && relInside(rel)
}

// StrictlyWithin is Within without the root itself: p must be below root.
func StrictlyWithin(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != "." && relInside(rel)
}

func relInside(rel string) bool {
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// WithinResolved is Within after following symlinks in both paths, so a link
// inside root that points out of it does not count. A missing root is not an
// error: nothing is inside a directory that does not exist. A missing p is.
func WithinResolved(root, p string) (bool, error) {
	rel, ok, err := resolvedRel(root, p)
	return ok && relInside(rel), err
}

// StrictlyWithinResolved is StrictlyWithin after following symlinks.
func StrictlyWithinResolved(root, p string) (bool, error) {
	rel, ok, err := resolvedRel(root, p)
	return ok && rel != "." && relInside(rel), err
}

func resolvedRel(root, p string) (string, bool, error) {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	resolvedPath, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", false, err
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedPath)
	if err != nil {
		return "", false, err
	}
	return rel, true, nil
}

// ExpandHome expands a leading "~" or "~/" to the user's home directory. Other
// paths come back unchanged apart from surrounding space being trimmed; an
// empty path is an error.
func ExpandHome(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", errors.New("path is required")
	}
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	if p == "~" {
		return home, nil
	}
	return filepath.Join(home, p[2:]), nil
}
