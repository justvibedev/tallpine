package claudeacp

import (
	"path/filepath"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/protectstate"
	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/domain"
)

// A Chat session's role overrides reach the SDK settings layer, merged with
// the protectState fence so neither drops the other; none means no settings.
func TestClaudeChatSessionCarriesRoleSettings(t *testing.T) {
	role := `{"autoMemoryEnabled":false,"enabledPlugins":{"impeccable@m":false}}`
	got, _ := sdkOptions(t, agentpolicy.Decision{}, map[string]string{domain.EnvClaudeRoleSettings: role})["settings"].(map[string]any)
	plugins, _ := got["enabledPlugins"].(map[string]any)
	if got["autoMemoryEnabled"] != false || plugins["impeccable@m"] != false {
		t.Fatalf("settings = %v", got)
	}

	withGOOS(t, "darwin")
	env := map[string]string{brand.Env("RUN_FILE"): filepath.Join(t.TempDir(), "running.json"), domain.EnvClaudeRoleSettings: role}
	on := policytest.Decision(t, map[string]map[string]any{protectstate.Name: {protectstate.FieldMode: protectstate.On}})
	fenced, ok := fenceSettings(t, on, env)
	if !ok || fenced["sandbox"] == nil || fenced["autoMemoryEnabled"] != false {
		t.Fatalf("fenced settings = %v, want sandbox and role keys", fenced)
	}

	if _, ok := sdkOptions(t, agentpolicy.Decision{}, nil)["settings"]; ok {
		t.Fatalf("no role settings still wrote settings")
	}
}
