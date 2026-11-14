package sessionartifacts

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// emptyTree lists dir and its subdirectories when the tree holds no files.
// ok is false when dir is missing or holds a file or symlink.
func emptyTree(dir string) (dirs []string, ok bool, err error) {
	if dir == "" {
		return nil, false, nil
	}
	hasFile := false
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			hasFile = true
			return fs.SkipAll
		}
		dirs = append(dirs, p)
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	return dirs, err == nil && !hasFile, err
}

// IsEmpty reports whether dir exists and holds no files (nested empty
// directories count as empty).
func IsEmpty(dir string) (bool, error) {
	_, ok, err := emptyTree(dir)
	return ok, err
}

// RemoveIfEmpty removes dir when it holds no files at all. A directory with
// any file or symlink is left untouched; os.Remove refuses a directory that
// gained a file meanwhile. It reports whether dir existed and was removed.
func RemoveIfEmpty(dir string) (bool, error) {
	dirs, ok, err := emptyTree(dir)
	if err != nil || !ok {
		return false, err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := os.Remove(dirs[i]); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return false, err
		}
	}
	return true, nil
}
