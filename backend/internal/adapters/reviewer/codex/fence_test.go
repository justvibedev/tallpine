package codex

import (
	"testing"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// The Codex reviewer runs in auto (workspace-write), which nests Codex's own
// seatbelt, so it cannot be fenced and keeps the gap in every mode.
func TestCodexReviewerKeepsFenceGap(t *testing.T) {
	for _, mode := range []ports.PermissionMode{ports.PermissionModeAuto, ports.PermissionModeBypassPermissions} {
		if ports.FencesLaunch(New(), "darwin", mode) {
			t.Fatalf("codex reviewer fenced for %q", mode)
		}
	}
}
