package claudecode

import (
	"encoding/json"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// addLaunchOptionSettings writes a session's launch options into its Claude
// Code --settings: Ultracode, the token count at which it compacts on its own,
// and the project's per-role plugin and auto-memory overrides. Unset options
// write nothing, so the account's own settings apply.
func addLaunchOptionSettings(settings map[string]any, options domain.AgentLaunchOptions) {
	if options.Ultracode {
		settings["ultracode"] = true
	}
	if tokens := domain.AutoCompactTokenCount(options.AutoCompactWindow, options.ModelContextWindow); tokens > 0 {
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
}
