package codexappserver

import (
	"fmt"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/statefence/fencetest"
)

// Thread start, resume, fork and /clear build their roots here.
func TestThreadWritableRootsTrustOnlyTheRecordedRepo(t *testing.T) {
	fencetest.AssertCodexRootsTrusted(t, func(_ *testing.T, d agentpolicy.Decision, ws string) string {
		return fmt.Sprint(threadStartParams(threadSettings{Policy: d, Permissions: ports.PermissionModeAcceptEdits, WorkspacePath: ws})["config"])
	})
}

// Every turn rebuilds its roots, so this path needs no relaunch to be reached.
func TestTurnWritableRootsTrustOnlyTheRecordedRepo(t *testing.T) {
	fencetest.AssertCodexRootsTrusted(t, func(_ *testing.T, d agentpolicy.Decision, ws string) string {
		params := map[string]any{}
		applyTurnSettings(params, ports.ChatTurnSettings{Approval: ports.PermissionModeAcceptEdits}, false, false, d, ws)
		return fmt.Sprint(params["sandboxPolicy"])
	})
}
