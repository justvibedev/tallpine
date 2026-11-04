package deepseek

import (
	"testing"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// The dsh reviewer runs pinned read-only, which nests dsh's own seatbelt, so it
// cannot be fenced and keeps the gap in every mode.
func TestDeepSeekReviewerKeepsFenceGap(t *testing.T) {
	for _, mode := range []ports.PermissionMode{ports.PermissionModeAuto, ports.PermissionModeBypassPermissions} {
		if ports.FencesLaunch(New(), "darwin", mode) {
			t.Fatalf("deepseek reviewer fenced for %q", mode)
		}
	}
}
