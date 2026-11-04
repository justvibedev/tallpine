package daemon

import (
	"path/filepath"
	"testing"
)

func TestClaudeDeviceFolderIgnoresInheritedConfigDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "managed"))
	gotHome, gotConfig := claudeDeviceFolder()
	if gotHome != filepath.Join(home, ".claude") || gotConfig != filepath.Join(home, ".claude.json") {
		t.Fatalf("device folder = %q, %q", gotHome, gotConfig)
	}
}
