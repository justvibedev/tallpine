package codexappserver

import (
	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// approvalSettings maps Tallpine's existing per-session permission mode onto Codex's
// approval policy and sandbox.
//
// The default matches what Tallpine already passes a Codex TUI session
// (--dangerously-bypass-approvals-and-sandbox): Tallpine sessions run in isolated
// worktrees and are expected to work without prompting. Chat does not quietly
// become stricter than the terminal path for the same setting.
func approvalSettings(decision agentpolicy.Decision, mode ports.PermissionMode) (policy, sandbox string) {
	switch ports.NormalizePermissionMode(permissioncap.Of(decision).LowerLive(domain.HarnessCodex, mode)) {
	case ports.PermissionModeAcceptEdits, ports.PermissionModeAuto:
		// on-request lets the provider decide when to ask; workspace-write keeps
		// edits inside the worktree.
		return "on-request", "workspace-write"
	default:
		return "never", "danger-full-access"
	}
}

// approvalReviewer selects whether Codex asks the user directly or first lets
// its built-in reviewer approve routine safe actions. Explicitly sending "user"
// also resets a thread that previously used auto review.
func approvalReviewer(decision agentpolicy.Decision, mode ports.PermissionMode) string {
	// Capped like approvalSettings: under accept-edits an auto turn must not
	// hand approvals to the built-in reviewer.
	if ports.NormalizePermissionMode(permissioncap.Of(decision).LowerLive(domain.HarnessCodex, mode)) == ports.PermissionModeAuto {
		return "auto_review"
	}
	return "user"
}

// SandboxAllowsNetwork reports whether this thread's agent can reach the
// network. Every sandbox Tallpine launches Codex in (full access, the
// read-only reviewer, and workspace-write, which gets network_access) has it.
func (c *conversation) SandboxAllowsNetwork(turnMode ports.PermissionMode) bool {
	if c.disableNetwork {
		return false
	}
	if c.readOnly {
		return true
	}
	if turnMode == "" {
		turnMode = c.launchMode
	}
	_, sandbox := approvalSettings(c.policy, turnMode)
	return sandbox == "danger-full-access" || sandbox == "workspace-write"
}

func launchApprovalSettings(decision agentpolicy.Decision, mode ports.PermissionMode, readOnly bool) (policy, sandbox, reviewer string) {
	if readOnly {
		return "never", "read-only", "user"
	}
	policy, sandbox = approvalSettings(decision, mode)
	return policy, sandbox, approvalReviewer(decision, mode)
}
