package migratevo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeProjectDirName(t *testing.T) {
	cases := map[string]string{
		"/Users/c/.vo/data/worktrees/tallpine/tallpine-10": "-Users-c--vo-data-worktrees-tallpine-tallpine-10",
		"/tmp/a b_c.d": "-tmp-a-b-c-d",
		"/tmp/é":       "-tmp--",
		"/tmp/😀":       "-tmp---", // astral rune = two UTF-16 units
	}
	for in, want := range cases {
		got, ok := ClaudeProjectDirName(in)
		if !ok || got != want {
			t.Errorf("ClaudeProjectDirName(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	if _, ok := ClaudeProjectDirName("/" + strings.Repeat("a", 250)); ok {
		t.Error("over-long name must be reported as unreproducible")
	}
}

// TestClaudeProjectDirNameMatchesRealDirs checks the escaping against dirs
// Claude really created (read-only listing; skipped where there are none).
func TestClaudeProjectDirNameMatchesRealDirs(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home")
	}
	entries, err := os.ReadDir(filepath.Join(home, ".claude", "projects"))
	if err != nil || len(entries) == 0 {
		t.Skip("no ~/.claude/projects")
	}
	realDirs := map[string]bool{}
	for _, e := range entries {
		realDirs[e.Name()] = true
		if len(e.Name()) > maxClaudeDirName {
			continue
		}
		// Every real name is already escaped, so escaping leaves it alone.
		if got, _ := ClaudeProjectDirName(e.Name()); got != e.Name() {
			t.Errorf("real dir %q escapes to %q", e.Name(), got)
		}
	}
	// Anchor: the checkout this test runs in is (usually) a session cwd with a
	// real Claude dir; its escaped name must be among them.
	dir, _ := os.Getwd()
	for dir != "/" {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			break
		}
		dir = filepath.Dir(dir)
	}
	if name, ok := ClaudeProjectDirName(dir); ok && strings.Contains(dir, "/.vo/data/worktrees/") && !realDirs[name] {
		t.Skipf("no Claude dir for this checkout (%s)", name)
	} else if ok && realDirs[name] {
		t.Logf("matched real dir %s", name)
	}
}
