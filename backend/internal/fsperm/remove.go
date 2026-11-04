package fsperm

import (
	"errors"
	"io/fs"
	"os"
)

// RemoveAllRepairing is os.RemoveAll that also removes trees whose directories
// were left without owner write permission (Go's module cache, renv and some
// package managers do this). A path that is already gone is success. On a
// permission failure it repairs the directories once (see
// RepairRemovePermissions) and retries once; a second failure is the real
// answer. It never follows symlinks: a link is removed, not its target.
func RemoveAllRepairing(path string) error {
	err := os.RemoveAll(path)
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if !errors.Is(err, fs.ErrPermission) || !RepairRemovePermissions(path) {
		return err
	}
	if err := os.RemoveAll(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
