package agentruntime

// ClaudePermissionMode is Claude Code's --permission-mode value for a Tallpine
// policy, or "" when the policy leaves Claude's own default in place.
func ClaudePermissionMode(policy PermissionPolicy) string {
	switch NormalizePermissionPolicy(policy) {
	case PermissionAcceptEdits:
		return "acceptEdits"
	case PermissionAuto:
		return "auto"
	case PermissionBypassPermissions:
		return "bypassPermissions"
	default:
		return ""
	}
}

// ClaudePermissionArgs maps Tallpine policy onto Claude Code flags.
func ClaudePermissionArgs(policy PermissionPolicy) []string {
	if mode := ClaudePermissionMode(policy); mode != "" {
		return []string{"--permission-mode", mode}
	}
	return nil
}
