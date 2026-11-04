package testenv

import (
	"os"
	"testing"
)

// ShortTempDir returns a per-test dir under /tmp, removed at cleanup. Use it
// for Unix-socket paths: t.TempDir() lives under $TMPDIR, and a long $TMPDIR
// (macOS, sandboxes) pushes the socket past the 104-byte sun_path limit.
func ShortTempDir(t testing.TB) string {
	t.Helper()
	dir, err := os.MkdirTemp(ShortBaseDir(), "tp-s")
	if err != nil {
		t.Fatalf("ShortTempDir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
