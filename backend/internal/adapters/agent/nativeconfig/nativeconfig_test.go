package nativeconfig

import (
	"path/filepath"
	"testing"
)

func TestCodexHomeExplicitEmptyFallsToEnvHome(t *testing.T) {
	t.Setenv("CODEX_HOME", "/daemon/codex")
	got, err := CodexHome(map[string]string{"CODEX_HOME": "", "HOME": "/h"})
	if err != nil || got != filepath.Join("/h", ".codex") {
		t.Fatalf("got %q, %v", got, err)
	}
	got, _ = CodexHome(nil)
	if got != filepath.Clean("/daemon/codex") {
		t.Fatalf("daemon env: %q", got)
	}
}

func TestClaudeConfigDirMissingHomeIsError(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("home", "")
	if dir, _, err := ClaudeConfigDir(nil); err == nil {
		t.Fatalf("want error, got relative/empty dir %q", dir)
	}
}

func TestClaudeConfigDirGlobalConfig(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	dir, global, err := ClaudeConfigDir(map[string]string{"HOME": "/h"})
	if err != nil || dir != "/h/.claude" || global != "/h/.claude.json" {
		t.Fatalf("default: %q %q %v", dir, global, err)
	}
	dir, global, _ = ClaudeConfigDir(map[string]string{"CLAUDE_CONFIG_DIR": "/c", "HOME": "/h"})
	if dir != "/c" || global != "/c/.claude.json" {
		t.Fatalf("explicit: %q %q", dir, global)
	}
}

func TestClaudeConfigDirUnsetNameIgnoresDaemonEnv(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/daemon/claude")
	env := map[string]string{"HOME": "/h", "TALLPINE_UNSET_ENV": "CLAUDE_CONFIG_DIR"}
	dir, global, err := ClaudeConfigDir(env)
	if err != nil || dir != filepath.Join("/h", ".claude") || global != filepath.Join("/h", ".claude.json") {
		t.Fatalf("got %q %q %v", dir, global, err)
	}
}
