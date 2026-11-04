package claudeacp

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"tallpine.eu/tallpine/backend/internal/adapters/agent/claudecode"
	acpdriver "tallpine.eu/tallpine/backend/internal/adapters/chatdriver/acp"
	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/protectstate"
	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func withGOOS(t *testing.T, goos string) {
	t.Helper()
	prev := sessionGOOS
	sessionGOOS = goos
	t.Cleanup(func() { sessionGOOS = prev })
}

func sdkOptions(t *testing.T, policy agentpolicy.Decision, env map[string]string) map[string]any {
	t.Helper()
	meta := claudeSessionMeta(acpdriver.LaunchConfig{Policy: policy, Env: env})
	code, _ := meta["claudeCode"].(map[string]any)
	options, _ := code["options"].(map[string]any)
	return options
}

func fenceSettings(t *testing.T, policy agentpolicy.Decision, env map[string]string) (map[string]any, bool) {
	t.Helper()
	options := sdkOptions(t, policy, env)
	settings, ok := options["settings"].(map[string]any)
	if _, sources := options["settingSources"]; sources != ok {
		t.Fatalf("settingSources present = %v, fence present = %v; they must go together", sources, ok)
	}
	return settings, ok
}

// claude-agent-acp 0.99.0 (dist/acp-agent.js, the session query) reads
// _meta.claudeCode.options.settings and passes it as the SDK settings option,
// the --settings flag layer; settingSources overrides its user/project/local default.
func TestClaudeChatSessionCarriesProtectStateFence(t *testing.T) {
	env := map[string]string{brand.Env("RUN_FILE"): filepath.Join(t.TempDir(), "running.json")}
	on := policytest.Decision(t, map[string]map[string]any{protectstate.Name: {protectstate.FieldMode: protectstate.On}})

	withGOOS(t, "darwin")
	got, ok := fenceSettings(t, on, env)
	if !ok || got["sandbox"] == nil || got["permissions"].(map[string]any)["deny"] == nil {
		t.Fatalf("protectState on (darwin): chat settings = %v, want sandbox and deny", got)
	}
	if sources, _ := sdkOptions(t, on, env)["settingSources"].([]string); len(sources) != 1 || sources[0] != "user" {
		t.Fatalf("protectState on: settingSources = %v, want [user] (project/local hooks run outside the sandbox)", sources)
	}
	a, _ := json.Marshal(got)
	b, _ := json.Marshal(claudecode.ProtectStateSettingsFor(env, on))
	if string(a) != string(b) {
		t.Fatalf("chat fence = %s, want the shared builder's %s", a, b)
	}
	if _, ok := fenceSettings(t, policytest.Decision(t, nil), env); ok {
		t.Fatal("protectState off: chat must carry no fence settings")
	}
	if _, ok := fenceSettings(t, on, map[string]string{}); ok {
		t.Fatal("no run file: chat must carry no fence settings")
	}
	for _, goos := range []string{"linux", "windows"} {
		withGOOS(t, goos)
		if _, ok := fenceSettings(t, on, env); ok {
			t.Fatalf("%s: chat must carry no fence settings", goos)
		}
	}
}

func TestClaudeChatDriverFencesStateOnMacOSOnly(t *testing.T) {
	d := &checkpointDriver{}
	var _ ports.StateFencer = d
	var _ ports.StateFencer = claudecode.New()
	if !d.FencesState("darwin") || d.FencesState("linux") || d.FencesState("windows") {
		t.Fatal("Claude chat must fence on darwin only")
	}
}
