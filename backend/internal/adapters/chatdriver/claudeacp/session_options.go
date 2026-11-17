package claudeacp

import (
	"encoding/json"
	"runtime"

	"tallpine.eu/tallpine/backend/internal/adapters/agent/claudecode"
	acpdriver "tallpine.eu/tallpine/backend/internal/adapters/chatdriver/acp"
	"tallpine.eu/tallpine/backend/internal/contextwindow"
	"tallpine.eu/tallpine/backend/internal/domain"
)

// sessionGOOS is the host OS the session fence is decided for; tests swap it.
var sessionGOOS = runtime.GOOS

// claudeSDKOptions are the Agent SDK options sent per session, so a choice
// made after the adapter started still applies.
func claudeSDKOptions(cfg acpdriver.LaunchConfig) map[string]any {
	options := map[string]any{}
	if window := cfg.Env[contextwindow.ClaudeAutoCompactWindowEnv]; window != "" {
		options["env"] = map[string]string{contextwindow.ClaudeAutoCompactWindowEnv: window}
	}
	if cfg.DisableTools {
		options["tools"] = []string{}
		options["strictMcpConfig"] = true
		options["disallowedTools"] = []string{"mcp__*"}
	}
	addProtectStateFence(options, cfg)
	addRoleSettings(options, cfg)
	return options
}

// addProtectStateFence fences a chat session exactly as the terminal launch is
// fenced when protectState applies. claude-agent-acp (0.99.0, dist/acp-agent.js,
// the session query) reads userProvidedOptions.settings and spreads it into the
// Agent SDK's settings option, which Claude Code loads as its --settings layer.
// Like the terminal's --setting-sources user (claudecode.policyRestrictsSessions),
// it loads user settings only: project and local hooks run outside the sandbox
// and their sandbox arrays merge in and widen it. The adapter defaults to
// user, project and local, and spreads these options after that default.
func addProtectStateFence(options map[string]any, cfg acpdriver.LaunchConfig) {
	if !claudecode.ProtectStateApplies(cfg.Policy, sessionGOOS) {
		return
	}
	if fence := claudecode.ProtectStateSettingsFor(cfg.Env, cfg.Policy); len(fence) > 0 {
		options["settings"] = fence
		options["settingSources"] = []string{"user"}
	}
}

// addRoleSettings merges the session role's plugin and auto-memory overrides
// (domain.EnvClaudeRoleSettings, set by the session manager) into the SDK
// settings layer, keeping any protectState fence keys already there.
func addRoleSettings(options map[string]any, cfg acpdriver.LaunchConfig) {
	raw := cfg.Env[domain.EnvClaudeRoleSettings]
	if raw == "" {
		return
	}
	var role map[string]any
	if json.Unmarshal([]byte(raw), &role) != nil || len(role) == 0 {
		return
	}
	settings, _ := options["settings"].(map[string]any)
	if settings == nil {
		settings = map[string]any{}
	}
	for k, v := range role {
		if k == "permissions" {
			v = mergePermissions(settings[k], v)
		}
		settings[k] = v
	}
	options["settings"] = settings
}

// mergePermissions combines two permissions objects, joining their deny lists
// so a role's rules (code intelligence mode) keep the protectState fence's.
func mergePermissions(base, role any) any {
	b, _ := base.(map[string]any)
	r, ok := role.(map[string]any)
	if b == nil || !ok {
		return role
	}
	out := map[string]any{}
	for k, v := range b {
		out[k] = v
	}
	for k, v := range r {
		out[k] = v
	}
	var deny []any
	for _, src := range []map[string]any{b, r} {
		switch list := src["deny"].(type) {
		case []string:
			for _, s := range list {
				deny = append(deny, s)
			}
		case []any:
			deny = append(deny, list...)
		}
	}
	if len(deny) > 0 {
		out["deny"] = deny
	}
	return out
}
