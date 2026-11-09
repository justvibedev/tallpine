package tmux

import (
	"os"
	"path/filepath"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentlaunch"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// The fence denies agentlaunch.EnvFileDir, so the file must land there.
func TestWriteLaunchEnvFileUsesTheFencedDir(t *testing.T) {
	path, err := writeLaunchEnvFile(ports.RuntimeConfig{Env: map[string]string{"TALLPINE_SESSION_TOKEN": "x"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	if filepath.Dir(path) != agentlaunch.EnvFileDir() {
		t.Fatalf("env file at %s, want under %s", path, agentlaunch.EnvFileDir())
	}
}
