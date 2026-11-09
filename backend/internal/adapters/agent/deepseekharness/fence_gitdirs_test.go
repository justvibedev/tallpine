package deepseekharness

import (
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/statefence"
	"tallpine.eu/tallpine/backend/internal/statefence/fencetest"
)

// Launch and restore both reach the profile through FenceUnsandboxed, as does
// the ACP chat driver.
func TestFenceUnsandboxedTrustsOnlyTheRecordedRepo(t *testing.T) {
	if !statefence.Available("darwin") {
		t.Skip("sandbox-exec not present")
	}
	oldGOOS := fenceGOOS
	t.Cleanup(func() { fenceGOOS = oldGOOS })
	fenceGOOS = "darwin"
	fencetest.AssertGitPathsTrusted(t, func(t *testing.T, d agentpolicy.Decision, env map[string]string, ws string) string {
		argv, err := FenceUnsandboxed([]string{"dsh"}, env, d, ws)
		if err != nil {
			t.Fatal(err)
		}
		return argv[2]
	})
}
