package testenv

import "testing"

// B463: two fenced sessions share the alias root but not $TMPDIR; their links must not collide.
func TestFencedShortBaseDirIsPerTmpdir(t *testing.T) {
	t.Setenv("TALLPINE_SOCKET_ALIAS_ROOT", t.TempDir())
	t.Setenv("TMPDIR", t.TempDir())
	a := fencedShortBaseDir()
	t.Setenv("TMPDIR", t.TempDir())
	b := fencedShortBaseDir()
	if a == "" || b == "" || a == b {
		t.Fatalf("links must be distinct per TMPDIR: %q %q", a, b)
	}
}
