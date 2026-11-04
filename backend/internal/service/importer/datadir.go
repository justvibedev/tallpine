package importer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
)

// ResolveDataDir turns a data dir argument into an absolute, symlink-resolved,
// cleaned path. A directory that does not exist yet keeps its absolute form.
// The two import services share this one rule, so the same-directory guard
// compares like with like.
func ResolveDataDir(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", apierr.Invalid("SOURCE_DATA_DIR_REQUIRED", "a data dir is required", nil)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", apierr.Invalid("INVALID_DATA_DIR", "data dir path is invalid", map[string]any{"path": p})
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		return filepath.Clean(resolved), nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return filepath.Clean(abs), nil
	}
	return "", apierr.Invalid("INVALID_DATA_DIR", "data dir path could not be resolved", map[string]any{"path": p})
}

// SameDataDir reports whether a and b name the same directory. It asks the
// filesystem (os.SameFile), so a case-variant or linked spelling of the same
// directory is caught where a string compare is not. When a path does not exist
// yet, it falls back to comparing the cleaned strings.
func SameDataDir(a, b string) (bool, error) {
	ai, aErr := os.Stat(a)
	bi, bErr := os.Stat(b)
	if aErr == nil && bErr == nil {
		return os.SameFile(ai, bi), nil
	}
	if aErr != nil && !errors.Is(aErr, os.ErrNotExist) {
		return false, apierr.Invalid("INVALID_DATA_DIR", "source data dir could not be read", map[string]any{"path": a})
	}
	if bErr != nil && !errors.Is(bErr, os.ErrNotExist) {
		return false, apierr.Invalid("INVALID_DATA_DIR", "target data dir could not be read", map[string]any{"path": b})
	}
	return filepath.Clean(a) == filepath.Clean(b), nil
}
