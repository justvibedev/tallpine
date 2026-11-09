package codex

import (
	"encoding/json"
	"os"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/pkg/agentruntime"
)

// capCodexPermission applies the launch's policy decision to a terminal launch.
// Under a cap that forbids bypass it lowers the mode and pins the sandbox the
// decision names (`--sandbox workspace-write`): the approval flags alone leave
// sandbox_mode to the user's ~/.codex/config.toml, which may say
// danger-full-access. Any launch that ends up non-bypass (accept-edits, auto)
// runs in workspace-write, capped or not, so it gets the network and writable
// roots; bypass has no sandbox and is left untouched.
func capCodexPermission(decision agentpolicy.Decision, permissions ports.PermissionMode, workspace string, providerArgs *[]string) ports.PermissionMode {
	limit := permissioncap.Of(decision)
	if sandbox := limit.PinnedCodexSandbox(); sandbox != "" {
		*providerArgs = append(*providerArgs, "--sandbox", sandbox)
		permissions = limit.Lower(domain.HarnessCodex, permissions)
	}
	switch agentruntime.NormalizePermissionPolicy(agentruntime.PermissionPolicy(permissions)) {
	case agentruntime.PermissionAcceptEdits, agentruntime.PermissionAuto:
		appendWorkspaceWriteExtras(providerArgs, decision.GitRepo(), workspace)
	}
	return permissions
}

func appendWorkspaceWriteExtras(providerArgs *[]string, repo, workspace string) {
	home, _ := os.UserHomeDir()
	network, roots := permissioncap.CodexWorkspaceWriteExtras(home, repo, workspace)
	if network {
		*providerArgs = append(*providerArgs, "-c", "sandbox_workspace_write.network_access=true")
	}
	if len(roots) > 0 {
		quoted, _ := json.Marshal(roots)
		*providerArgs = append(*providerArgs, "-c", "sandbox_workspace_write.writable_roots="+string(quoted))
	}
}
