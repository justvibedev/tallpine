package domain

import (
	"encoding/json"
	"strconv"
	"strings"

	"tallpine.eu/tallpine/backend/internal/brand"
)

// ClaudeLaunchFor returns the Claude Code launch options the project's
// per-role claudePlugins and claudeAutoMemory settings give a role. Unset
// roles return zero options, which leave the launch unchanged.
func (c ProjectConfig) ClaudeLaunchFor(role string) (enabledPlugins string, autoMemoryOff bool) {
	plugins := map[string]bool{}
	for k, v := range c.ClaudePlugins[role] {
		if k = strings.TrimSpace(k); k != "" {
			plugins[k] = v
		}
	}
	if len(plugins) > 0 {
		encoded, _ := json.Marshal(plugins) // sorted keys: canonical
		enabledPlugins = string(encoded)
	}
	if on, ok := c.ClaudeAutoMemory[role]; ok && !on {
		autoMemoryOff = true
	}
	return enabledPlugins, autoMemoryOff
}

// PromptCacheTTLFor returns the CLAUDE_CODE_PROMPT_CACHE_TTL value for a role,
// or "" when the variable should stay unset.
func (c ProjectConfig) PromptCacheTTLFor(role string) string {
	if v, ok := c.PromptCacheTTL[role]; ok {
		return strings.TrimSpace(v)
	}
	if role == RoleReviewer {
		return "5m"
	}
	return ""
}

// EnvClaudeRoleSettings carries a Chat session's role overrides (the JSON
// object ClaudeRoleSettingsJSON returns) to the claude-agent-acp driver, which
// merges it into the session's SDK settings. Terminal launches get the same
// keys through AgentLaunchOptions instead.
var EnvClaudeRoleSettings = brand.Env("CLAUDE_ROLE_SETTINGS")

// SessionRoleFor is the per-role settings key for a worker or orchestrator.
func SessionRoleFor(kind SessionKind) string {
	if kind == KindOrchestrator {
		return "orchestrator"
	}
	return "worker"
}

// ClaudeRoleSettingsJSON returns the Claude Code settings object
// ({"enabledPlugins":...,"autoMemoryEnabled":false}) the project's per-role
// settings give a role, or "" when the role has none.
func (c ProjectConfig) ClaudeRoleSettingsJSON(role string) string {
	plugins, _ := c.ClaudeLaunchFor(role)
	return c.ClaudeRoleSettingsJSONWith(role, plugins, nil)
}

// ClaudeRoleSettingsJSONWith is ClaudeRoleSettingsJSON with the role's
// resolved enabledPlugins override and switched-off skills (skillOverrides),
// which need the skill inventory.
func (c ProjectConfig) ClaudeRoleSettingsJSONWith(role, plugins string, skillsOff []string) string {
	_, off := c.ClaudeLaunchFor(role)
	settings := map[string]json.RawMessage{}
	if plugins != "" {
		settings["enabledPlugins"] = json.RawMessage(plugins)
	}
	if off {
		settings["autoMemoryEnabled"] = json.RawMessage("false")
	}
	if overrides := ClaudeSkillOverrides(skillsOff); overrides != nil {
		encoded, _ := json.Marshal(overrides)
		settings["skillOverrides"] = encoded
	}
	if limit := ShellOutputLimit(c.Env); limit > 0 {
		// Chat sessions take the shell output cap here; terminal launches take
		// it from AgentLaunchOptions.ShellOutputChars.
		settings["bashOutputMaxChars"] = json.RawMessage(strconv.Itoa(limit))
	}
	if len(settings) == 0 {
		return ""
	}
	encoded, _ := json.Marshal(settings)
	return string(encoded)
}

// ClaudeSkillOverrides is the Claude Code skillOverrides value hiding the off
// skills, or nil for none. Claude Code 2.1 reads {name: "off"} (hidden from
// the model and from /name), with --settings among its top-priority sources.
func ClaudeSkillOverrides(off []string) map[string]string {
	if len(off) == 0 {
		return nil
	}
	overrides := make(map[string]string, len(off))
	for _, name := range off {
		overrides[name] = "off"
	}
	return overrides
}
