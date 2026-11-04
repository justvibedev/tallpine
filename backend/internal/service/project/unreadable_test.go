package project_test

import (
	"os"
	"testing"
)

// unreadable makes path unreadable, which fails `git add` after `git init` has
// run, and returns the call that restores it. An inherited GIT_INDEX_FILE can
// no longer cause that failure because git calls scrub it.
func unreadable(t *testing.T, path string) (restore func()) {
	t.Helper()
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	restore = func() {
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(restore)
	return restore
}
