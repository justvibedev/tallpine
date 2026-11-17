package claudecode

import (
	"encoding/json"
	"slices"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// addLaunchOptionSettings writes a session's launch options into its Claude
// Code --settings: Ultracode, the token count at which it compacts on its own,
// and the project's per-role plugin, auto-memory and skill overrides. Unset options
// write nothing, so the account's own settings apply.
func addLaunchOptionSettings(settings map[string]any, options domain.AgentLaunchOptions) {
	if options.Ultracode {
		settings["ultracode"] = true
	}
	if tokens := domain.ResolveAutoCompactWindow(options.AutoCompactWindow, options.ModelContextWindow, options.AutoCompactBaseline); tokens > 0 {
		settings["autoCompactEnabled"] = true
		settings["autoCompactWindow"] = tokens
	}
	if options.EnabledPlugins != "" {
		settings["enabledPlugins"] = json.RawMessage(options.EnabledPlugins)
	}
	if options.ShellOutputChars > 0 {
		// Output past the cap is saved to a file; Claude gets a preview and the
		// path. Failed commands are not cut by this setting.
		settings["bashOutputMaxChars"] = options.ShellOutputChars
	}
	if options.AutoMemoryOff {
		settings["autoMemoryEnabled"] = false
	}
	if overrides := domain.ClaudeSkillOverrides(options.SkillLaunch().Off); overrides != nil {
		settings["skillOverrides"] = overrides
	}
	if options.CodeIntel {
		addCodeIntelDeny(settings)
	}
}

// addCodeIntelDeny merges domain.CodeIntelDeny into settings' permissions.deny,
// keeping the rules the protectState fence and the credential guard added.
func addCodeIntelDeny(settings map[string]any) {
	perms, _ := settings["permissions"].(map[string]any)
	if perms == nil {
		perms = map[string]any{}
		settings["permissions"] = perms
	}
	var deny []string
	switch cur := perms["deny"].(type) {
	case []string:
		deny = cur
	case []any:
		for _, v := range cur {
			if s, ok := v.(string); ok {
				deny = append(deny, s)
			}
		}
	}
	for _, rule := range domain.CodeIntelDeny {
		if !slices.Contains(deny, rule) {
			deny = append(deny, rule)
		}
	}
	perms["deny"] = deny
}
