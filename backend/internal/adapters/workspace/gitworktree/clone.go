package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
)

// errCloneUnsupported is returned by cloneDir on platforms without a
// whole-directory copy-on-write clone primitive.
var errCloneUnsupported = errors.New("gitworktree: directory clone unsupported on this platform")

// cloneDirFunc is the platform clone primitive (clone_darwin.go or
// clone_other.go); a variable so tests can force the fallback path.
var cloneDirFunc = cloneDir

// tryCloneAssetDir seeds the directory source into the not-yet-existing
// destination with one copy-on-write clone. It reports whether the clone was
// made. Any false means the caller must use the normal copy walk, which also
// skips unsafe or vanished children individually.
//
// A clone duplicates everything beneath the directory, so the tree is scanned
// first with the same confinement rules the copy walk enforces. Entries the copy
// walk would skip (absolute, escaping or dangling symlinks, special files, and
// .git/.tallpine entries) do not veto the clone: they are removed from the clone, so
// one stray link such as a dangling node_modules/.bin entry does not disable
// the optimisation. The scan and the clone are not atomic; like the copy walk,
// this trusts the main checkout not to be rewritten concurrently.
func tryCloneAssetDir(ctx context.Context, sourceRoot, source, destination string) bool {
	if !cloneSupported {
		return false
	}
	skipped, err := scanCloneTree(ctx, sourceRoot, source)
	if err != nil {
		slog.InfoContext(ctx, "gitworktree: not cloning asset directory", "source", source, "error", err)
		return false
	}
	if err := cloneDirFunc(source, destination); err != nil {
		// clonefile creates nothing on failure, but never leave a partial
		// tree that the fallback would then merge into. EEXIST means someone
		// else owns the destination, so leave it alone.
		if !errors.Is(err, fs.ErrExist) {
			_ = os.RemoveAll(destination)
		}
		slog.InfoContext(ctx, "gitworktree: asset directory clone failed, copying instead", "source", source, "error", err)
		return false
	}
	if err := finishClone(destination, skipped); err != nil {
		_ = os.RemoveAll(destination)
		slog.InfoContext(ctx, "gitworktree: asset directory clone could not be finalised, copying instead", "source", source, "error", err)
		return false
	}
	if len(skipped) > 0 {
		slog.DebugContext(ctx, "gitworktree: omitted unsafe entries from cloned asset directory", "source", source, "count", len(skipped), "first", skipped[0])
	}
	return true
}

// finishClone makes the clone match what the copy walk would have produced:
// directories are owner-writable (clonefile preserves source modes, and e.g. a
// Go module cache is 0555, which would make the worktree impossible to remove)
// and the skipped entries are gone.
func finishClone(destination string, skipped []string) error {
	// chmod through a root handle so a path swapped for a symlink mid-walk
	// cannot redirect the mode change outside the clone.
	root, err := os.OpenRoot(destination)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	err = filepath.WalkDir(destination, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if perm := info.Mode().Perm(); perm&0o700 != 0o700 {
			rel, err := filepath.Rel(destination, path)
			if err != nil {
				return err
			}
			return root.Chmod(rel, perm|0o700)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, rel := range skipped {
		if err := os.RemoveAll(filepath.Join(destination, rel)); err != nil {
			return err
		}
	}
	return nil
}

// scanCloneTree walks source without following symlinks and returns the
// source-relative paths a whole-directory clone would copy but the copy walk
// would skip. A walk failure (or an unsafe root) is an error.
func scanCloneTree(ctx context.Context, sourceRoot, source string) ([]string, error) {
	var skipped []string
	err := filepath.WalkDir(source, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		skip := func() error {
			rel, relErr := filepath.Rel(source, path)
			if relErr != nil {
				return relErr
			}
			skipped = append(skipped, rel)
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if hasWorkspaceManagedComponent(d.Name()) {
			if path == source {
				return fmt.Errorf("managed entry %q: %w", path, ErrUnsafePath)
			}
			return skip()
		}
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			if _, err := validateAssetSymlink(sourceRoot, path); err != nil {
				if errors.Is(err, ErrUnsafePath) {
					return skip()
				}
				return err
			}
			return nil
		case d.IsDir() || d.Type().IsRegular():
			return nil
		default:
			return skip()
		}
	})
	return skipped, err
}
