package kiro

import (
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/statefence"
	"tallpine.eu/tallpine/backend/internal/statefence/fencetest"
)

// Launch, restore, the ACP chat driver and the reviewer all call Fence.
func TestFenceTrustsOnlyTheRecordedRepo(t *testing.T) {
	if !statefence.Available("darwin") {
		t.Skip("sandbox-exec not present")
	}
	oldGOOS := fenceGOOS
	t.Cleanup(func() { fenceGOOS = oldGOOS })
	fenceGOOS = "darwin"
	fencetest.AssertGitPathsTrusted(t, func(t *testing.T, d agentpolicy.Decision, env map[string]string, ws string) string {
		argv, err := Fence([]string{"kiro"}, env, d, ws)
		if err != nil {
			t.Fatal(err)
		}
		return argv[2]
	})
}
