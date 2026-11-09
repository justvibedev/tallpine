// Package fsperm locks down the permissions of the daemon's data root so other
// local users cannot read conversations, the database, artifacts or worktrees.
package fsperm

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	dirMode  fs.FileMode = 0o700
	fileMode fs.FileMode = 0o600
)

// Directories Tallpine owns under the data dir. Every directory directly under
// the state root is Tallpine's too, so those are tightened without a list.
var dataSubdirs = []string{"worktrees", "runtime", "skills"}

// Harden creates the state root and the data dir as 0700 and tightens the
// known Tallpine subdirectories and every directory directly under a state root
// distinct from dataDir (only those that already exist; none are created) in
// place. Inside dataDir it chmods
// the top-level files (database, -wal, -shm, ...) to 0600 and walks the
// artifacts tree. Group/other bits are cleared and owner bits kept. Failure to
// create the roots is the only error; tightening problems come back as
// warnings so an existing install still starts. Worktree and other subdirectories get only their directory
// mode: their contents are user source trees and are never touched. Symlinks
// are never followed or chmodded, and nothing outside stateDir/dataDir is
// modified.
func Harden(stateDir, dataDir string) (warnings []error, err error) {
	roots := []string{stateDir}
	if dataDir != stateDir {
		roots = append(roots, dataDir)
	}
	for _, d := range roots {
		if err := os.MkdirAll(d, dirMode); err != nil {
			return nil, err
		}
	}
	var errs []error
	for _, d := range roots {
		errs = append(errs, chmodIfLoose(d))
	}
	for _, name := range dataSubdirs {
		errs = append(errs, chmodIfLoose(filepath.Join(dataDir, name)))
	}
	if stateDir != dataDir {
		errs = append(errs, tightenDirs(stateDir))
	}
	errs = append(errs, tightenFiles(dataDir), tightenTree(filepath.Join(dataDir, "artifacts")))
	for _, e := range errs {
		if e != nil {
			warnings = append(warnings, e)
		}
	}
	return warnings, nil
}

// chmodIfLoose clears the group and other bits of a real (non-symlink) path,
// keeping the owner bits (an executable stays executable).
func chmodIfLoose(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	perm := info.Mode().Perm()
	if perm&0o077 == 0 {
		return nil
	}
	return os.Chmod(path, perm&^0o077)
}

// tightenDirs chmods the real directories directly inside dir, never their
// contents; a symlink's IsDir is false.
func tightenDirs(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	var errs []error
	for _, e := range entries {
		if e.IsDir() {
			errs = append(errs, chmodIfLoose(filepath.Join(dir, e.Name())))
		}
	}
	return errors.Join(errs...)
}

// tightenFiles chmods the regular files directly inside dir.
func tightenFiles(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	var errs []error
	for _, e := range entries {
		if e.Type().IsRegular() {
			errs = append(errs, chmodIfLoose(filepath.Join(dir, e.Name())))
		}
	}
	return errors.Join(errs...)
}

// tightenTree chmods every directory and regular file under root.
func tightenTree(root string) error {
	var errs []error
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, err)
			}
			return nil
		}
		switch {
		case d.IsDir():
			errs = append(errs, chmodIfLoose(path))
		case d.Type().IsRegular():
			errs = append(errs, chmodIfLoose(path))
		}
		return nil
	})
	return errors.Join(errs...)
}

// CreatePrivateFile creates an empty 0600 file at path if none exists, so a
// file a library would otherwise create with the process umask (the SQLite
// database, whose -wal/-shm inherit its mode) starts owner-only. An existing
// file is left untouched.
func CreatePrivateFile(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return f.Close()
}
