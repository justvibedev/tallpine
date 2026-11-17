package ports

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReviewerNeutralDirIsPerHarnessUnderTheTaskPromptRoot(t *testing.T) {
	root := t.TempDir()
	dir, err := ReviewerNeutralDir(root, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "cwd", "codex"); dir != want {
		t.Fatalf("dir = %q, want %q", dir, want)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("dir not created: %v", err)
	}
	for _, bad := range []string{"", "  ", "relative/root"} {
		if _, err := ReviewerNeutralDir(bad, "codex"); err == nil {
			t.Fatalf("root %q accepted", bad)
		}
	}
}
