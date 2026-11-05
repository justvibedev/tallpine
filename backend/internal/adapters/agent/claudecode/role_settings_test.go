package claudecode

import (
	"encoding/json"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
)

func launchSettings(t *testing.T, options domain.AgentLaunchOptions) map[string]any {
	t.Helper()
	args := claudeLaunchSettingsArgs(options, "", nil, agentpolicy.Decision{})
	var settings map[string]any
	if err := json.Unmarshal([]byte(args[1]), &settings); err != nil {
		t.Fatalf("settings %q: %v", args[1], err)
	}
	return settings
}

// The role's plugin override and auto-memory switch reach Claude Code through
// --settings; without them neither key is written, so the account wins.
func TestLaunchSettingsCarryRolePluginsAndAutoMemory(t *testing.T) {
	got := launchSettings(t, domain.AgentLaunchOptions{EnabledPlugins: `{"impeccable@m":false}`, AutoMemoryOff: true})
	plugins, _ := got["enabledPlugins"].(map[string]any)
	if plugins["impeccable@m"] != false || got["autoMemoryEnabled"] != false {
		t.Fatalf("settings = %v", got)
	}
	plain := launchSettings(t, domain.AgentLaunchOptions{})
	if _, ok := plain["enabledPlugins"]; ok {
		t.Fatalf("unset plugins written: %v", plain)
	}
	if _, ok := plain["autoMemoryEnabled"]; ok {
		t.Fatalf("unset auto-memory written: %v", plain)
	}
}
