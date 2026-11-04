package claudecode

import (
	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/pkg/agentruntime"
)

// claudePolicyModeArgs makes Claude's default mode explicit under a policy limit.
// Tallpine "default" passes no --permission-mode, so Claude resolves the starting mode
// from the user's settings, where permissions.defaultMode may be
// bypassPermissions. Only active under a cap that forbids bypass; otherwise no
// flag is added and the launch is unchanged. The mode string comes from the same
// helper as the ACP chat path, but only "default" is added here: the policy's own
// flag is already placed by agentruntime.
func claudePolicyModeArgs(decision agentpolicy.Decision, policy agentruntime.PermissionPolicy) []string {
	if permissioncap.Of(decision).ClaudeMode(ports.PermissionMode(policy), true) != "default" {
		return nil
	}
	return []string{"--permission-mode", "default"}
}
