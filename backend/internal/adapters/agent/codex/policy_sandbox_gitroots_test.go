package codex

import (
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/statefence/fencetest"
)

// Terminal launch and restore in accept-edits or auto reach the roots here.
func TestLaunchWritableRootsTrustOnlyTheRecordedRepo(t *testing.T) {
	fencetest.AssertCodexRootsTrusted(t, func(_ *testing.T, d agentpolicy.Decision, ws string) string {
		var args []string
		capCodexPermission(d, ports.PermissionModeAcceptEdits, ws, &args)
		return strings.Join(args, " ")
	})
}
