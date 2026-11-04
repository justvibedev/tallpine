//go:build !windows

package sqlite

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestOpenCreatesPrivateDatabaseFiles(t *testing.T) {
	old := syscall.Umask(0)
	t.Cleanup(func() { syscall.Umask(old) })
	dir := filepath.Join(t.TempDir(), "data")
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	for _, name := range []string{"ao.db", "ao.db-wal", "ao.db-shm"} {
		fi, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			continue // the sidecars may not exist yet
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("%s = %o, want 600", name, fi.Mode().Perm())
		}
	}
	if fi, err := os.Stat(filepath.Join(dir, "ao.db")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("ao.db missing or not 600: %v", err)
	}
}
