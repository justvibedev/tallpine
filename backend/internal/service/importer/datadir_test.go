package importer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveDataDir(t *testing.T) {
	if _, err := ResolveDataDir("  "); err == nil {
		t.Fatal("blank data dir should be rejected")
	}
	base := t.TempDir()
	realDir := filepath.Join(base, "real")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	physical, _ := filepath.EvalSymlinks(realDir)
	link := filepath.Join(base, "link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if got, err := ResolveDataDir(link + "/"); err != nil || got != physical {
		t.Fatalf("linked dir = %q,%v want %q", got, err, physical)
	}
	missing := filepath.Join(base, "missing")
	if got, err := ResolveDataDir(missing); err != nil || got != filepath.Clean(missing) {
		t.Fatalf("missing dir = %q,%v", got, err)
	}
}

func TestSameDataDir(t *testing.T) {
	base := t.TempDir()
	a, b := filepath.Join(base, "a"), filepath.Join(base, "b")
	for _, d := range []string{a, b} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if same, err := SameDataDir(a, b); err != nil || same {
		t.Fatalf("distinct dirs: %v,%v", same, err)
	}
	if same, err := SameDataDir(a, a); err != nil || !same {
		t.Fatalf("same dir: %v,%v", same, err)
	}
	if same, err := SameDataDir(filepath.Join(base, "x"), filepath.Join(base, "x")); err != nil || !same {
		t.Fatalf("same missing dir: %v,%v", same, err)
	}
	// A case-variant spelling differs as a string but is one directory on a
	// case-insensitive filesystem; only the filesystem can say so.
	variant := filepath.Join(base, strings.ToUpper("a"))
	if _, err := os.Stat(variant); err == nil {
		if same, err := SameDataDir(a, variant); err != nil || !same {
			t.Fatalf("case variant: %v,%v", same, err)
		}
	}
}
