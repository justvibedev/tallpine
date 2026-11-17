package cli

import (
	"fmt"
	"strings"
)

// claudePluginRoles are the session roles claudePlugins is keyed by.
var claudePluginRoles = map[string]bool{"worker": true, "orchestrator": true, "reviewer": true}

// setClaudePlugins applies --claude-plugin role=id:on|off|unset specs to the
// config's claudePlugins map. Only the named role/plugin pairs change; the
// rest of the map, and every other config field, is kept. "unset" drops the
// plugin's override so the role follows its default again.
func setClaudePlugins(cfg map[string]any, specs []string) error {
	type edit struct {
		role, id string
		on, drop bool
	}
	edits := make([]edit, 0, len(specs))
	for _, spec := range specs {
		role, rest, ok := strings.Cut(spec, "=")
		role = strings.TrimSpace(role)
		i := strings.LastIndex(rest, ":")
		if !ok || i < 0 {
			return usageError{fmt.Errorf("--claude-plugin %q: want role=id:on|off|unset", spec)}
		}
		id, state := strings.TrimSpace(rest[:i]), strings.ToLower(strings.TrimSpace(rest[i+1:]))
		if !claudePluginRoles[role] {
			return usageError{fmt.Errorf("--claude-plugin %q: role must be worker, orchestrator or reviewer", spec)}
		}
		if id == "" {
			return usageError{fmt.Errorf("--claude-plugin %q: plugin id is empty", spec)}
		}
		switch state {
		case "on", "off", "unset":
			edits = append(edits, edit{role, id, state == "on", state == "unset"})
		default:
			return usageError{fmt.Errorf("--claude-plugin %q: state must be on, off or unset", spec)}
		}
	}
	for _, e := range edits {
		if e.drop {
			if m, ok := cfg["claudePlugins"].(map[string]any); ok {
				deleteNestedConfig(m, e.role, e.id)
				pruneEmptyConfig(cfg, "claudePlugins")
			}
			continue
		}
		nestedConfig(nestedConfig(cfg, "claudePlugins"), e.role)[e.id] = e.on
	}
	return nil
}
