//go:build darwin

package previewserver

import (
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/statefence"
	"tallpine.eu/tallpine/backend/internal/statefence/fencetest"
)

// A preview start, human or agent, builds its profile here at that moment.
func TestPreviewFenceTrustsOnlyTheRecordedRepo(t *testing.T) {
	if !statefence.Available("darwin") {
		t.Skip("sandbox-exec not present")
	}
	fencetest.AssertGitPathsTrusted(t, func(t *testing.T, d agentpolicy.Decision, env map[string]string, ws string) string {
		profile, _, err := Fence{Decision: d, RunFile: env[brand.Env("RUN_FILE")], DataDir: env[brand.Env("DATA_DIR")], SessionID: "s-1"}.resolve(ws)
		if err != nil {
			t.Fatal(err)
		}
		return profile
	})
}
