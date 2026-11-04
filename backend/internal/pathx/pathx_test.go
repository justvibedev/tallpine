package pathx

import (
	"os"
	"path/filepath"
	"testing"
)

func TestContainmentTable(t *testing.T) {
	cases := []struct {
		name, root, p  string
		within, strict bool
	}{
		{"root itself", "/a/b", "/a/b", true, false},
		{"root with trailing slash", "/a/b/", "/a/b", true, false},
		{"child", "/a/b", "/a/b/c", true, true},
		{"dot segment is root", "/a/b", "/a/b/.", true, false},
		{"dotdot escapes", "/a/b", "/a/b/../c", false, false},
		{"parent", "/a/b", "/a", false, false},
		{"dotdot-prefixed name stays inside", "/a/b", "/a/b/..c", true, true},
		{"prefix sibling", "/a/b", "/a/bc", false, false},
		{"prefix sibling child", "/a/b", "/a/bc/d", false, false},
		{"relative vs absolute", "/a/b", "c", false, false},
	}
	for _, c := range cases {
		if got := Within(c.root, c.p); got != c.within {
			t.Errorf("%s: Within(%q,%q)=%v want %v", c.name, c.root, c.p, got, c.within)
		}
		if got := StrictlyWithin(c.root, c.p); got != c.strict {
			t.Errorf("%s: StrictlyWithin(%q,%q)=%v want %v", c.name, c.root, c.p, got, c.strict)
		}
	}
}

func TestResolvedContainment(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	for _, d := range []string{filepath.Join(root, "in"), outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if err := os.Symlink(root, filepath.Join(base, "rootlink")); err != nil {
		t.Fatal(err)
	}

	in := filepath.Join(root, "in")
	cases := []struct {
		name, p        string
		within, strict bool
	}{
		{"child", in, true, true},
		{"root", root, true, false},
		{"symlink escape", filepath.Join(root, "escape"), false, false},
		{"root via link", filepath.Join(base, "rootlink"), true, false},
		{"child via root link", filepath.Join(base, "rootlink", "in"), true, true},
	}
	for _, c := range cases {
		got, err := WithinResolved(root, c.p)
		if err != nil || got != c.within {
			t.Errorf("%s: WithinResolved=%v,%v want %v", c.name, got, err, c.within)
		}
		got, err = StrictlyWithinResolved(root, c.p)
		if err != nil || got != c.strict {
			t.Errorf("%s: StrictlyWithinResolved=%v,%v want %v", c.name, got, err, c.strict)
		}
	}
	if ok, err := WithinResolved(filepath.Join(base, "missing"), in); ok || err != nil {
		t.Errorf("missing root: %v,%v want false,nil", ok, err)
	}
	if _, err := WithinResolved(root, filepath.Join(root, "nope")); err == nil {
		t.Error("missing path should error")
	}
}

func TestExpandHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for in, want := range map[string]string{
		"~":        home,
		"~/x/y":    filepath.Join(home, "x", "y"),
		" ~/x ":    filepath.Join(home, "x"),
		"/abs":     "/abs",
		"~other/x": "~other/x",
	} {
		if got, err := ExpandHome(in); err != nil || got != want {
			t.Errorf("ExpandHome(%q)=%q,%v want %q", in, got, err, want)
		}
	}
	if _, err := ExpandHome("  "); err == nil {
		t.Error("empty path should error")
	}
}
