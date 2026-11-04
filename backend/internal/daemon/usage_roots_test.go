package daemon

import (
	"context"
	"path/filepath"
	"testing"
)

func TestUsageSourceRootsFollowEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	got, err := usageSourceRoots(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".claude", "projects"); got.ClaudeProjects != want {
		t.Fatalf("claude = %q, want %q", got.ClaudeProjects, want)
	}
	if want := filepath.Join(home, ".codex", "sessions"); got.CodexSessions != want {
		t.Fatalf("codex = %q, want %q", got.CodexSessions, want)
	}
	custom := filepath.Join(home, "cfg")
	t.Setenv("CLAUDE_CONFIG_DIR", custom)
	t.Setenv("CODEX_HOME", filepath.Join(home, "cx"))
	got, _ = usageSourceRoots(context.Background())
	if got.ClaudeProjects != filepath.Join(custom, "projects") || got.CodexArchived != filepath.Join(home, "cx", "archived_sessions") {
		t.Fatalf("custom roots wrong: %+v", got)
	}
}
