package claudecode

import "tallpine.eu/tallpine/backend/pkg/agentruntime"

// launchModeSettings is the part of a launch's flag settings that follows from
// the Tallpine permission mode: the auto-mode dialog guard (see
// autoDefaultNudgeGuardArgs) and, for bypass, the bypass-mode consent.
func launchModeSettings(policy agentruntime.PermissionPolicy) map[string]any {
	settings := autoDefaultNudgeGuardSettings(policy)
	if settings == nil {
		settings = map[string]any{}
	}
	acceptBypassDisclaimer(settings, policy)
	return settings
}

// acceptBypassDisclaimer records consent to bypass mode in the flag settings
// when Tallpine itself starts the session in bypassPermissions. Without a
// skipDangerousModePermissionPrompt in some settings layer, Claude Code
// (verified in 2.1.317) shows a blocking "Bypass Permissions mode" dialog during
// startup onboarding. Nothing in Tallpine answers it, so the session sits at 0
// turns in needs_input (B437). The flag layer counts as consent for that
// dialog, and the project's bypass setting is the user's explicit choice.
func acceptBypassDisclaimer(settings map[string]any, policy agentruntime.PermissionPolicy) {
	if agentruntime.ClaudePermissionMode(policy) == "bypassPermissions" {
		settings["skipDangerousModePermissionPrompt"] = true
	}
}
