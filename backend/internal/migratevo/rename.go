package migratevo

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// RenameHome moves the whole source tree to the target in one rename(2).
// Nothing is left behind in $HOME: a README-only stub from an older run is
// removed. Safe to call again after a crash: if the target is already in
// place it only finishes the cleanup.
func (e *Env) RenameHome() error {
	_, tErr := os.Lstat(e.Target)
	switch {
	case errors.Is(tErr, os.ErrNotExist):
		if err := os.Rename(e.Source, e.Target); err != nil {
			return fmt.Errorf("rename %s -> %s: %w", e.Source, e.Target, err)
		}
	case tErr != nil:
		return tErr
	default:
		if entries, err := os.ReadDir(e.Source); err == nil {
			for _, ent := range entries {
				if ent.Name() != readmeName {
					return fmt.Errorf("both %s and %s hold data; refusing to guess", e.Source, e.Target)
				}
			}
		}
	}
	return e.removeStub()
}

// removeStub deletes a README-only old root; anything else is left alone.
func (e *Env) removeStub() error {
	entries, err := os.ReadDir(e.Source)
	if errors.Is(err, os.ErrNotExist) {
		return nil // already gone
	}
	if err != nil {
		return err
	}
	for _, ent := range entries {
		if ent.Name() != readmeName {
			return nil
		}
	}
	if err := os.Remove(filepath.Join(e.Source, readmeName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Remove(e.Source)
}
