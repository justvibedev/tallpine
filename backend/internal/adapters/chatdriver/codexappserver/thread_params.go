package codexappserver

import (
	"os"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// threadSettings are the launch settings every thread/start, thread/resume and
// thread/fork request carries. threadStartParams is the one place that turns them
// into request parameters, so a setting added here reaches launch, resume and
// fork alike.
type threadSettings struct {
	WorkspacePath string
	Policy        agentpolicy.Decision
	Permissions   ports.PermissionMode
	ReadOnly      bool
	Model         string
	Effort        string
	MCPServers    []ports.ChatMCPServerConfig
	Env           map[string]string
	SystemPrompt  string
}

func threadStartParams(s threadSettings) map[string]any {
	policy, sandbox, reviewer := launchApprovalSettings(s.Policy, s.Permissions, s.ReadOnly)
	params := map[string]any{
		"cwd":               s.WorkspacePath,
		"approvalPolicy":    policy,
		"approvalsReviewer": reviewer,
		"sandbox":           sandbox,
	}
	if s.Model != "" {
		params["model"] = s.Model
	}
	// There is no top-level effort field; carry the durable Tallpine choice as a
	// config override so a thread does not fall back to the provider default.
	config := threadConfig(s.Effort, s.MCPServers, s.Env)
	if sandbox == "workspace-write" {
		home, _ := os.UserHomeDir()
		network, roots := permissioncap.CodexWorkspaceWriteExtras(home, s.Policy.GitRepo(), s.WorkspacePath)
		ws := map[string]any{"network_access": network}
		if len(roots) > 0 {
			ws["writable_roots"] = roots
		}
		config["sandbox_workspace_write"] = ws
	}
	if len(config) > 0 {
		params["config"] = config
	}
	if s.SystemPrompt != "" {
		params["developerInstructions"] = s.SystemPrompt
	}
	return params
}
