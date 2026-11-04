package sessionmanager

import (
	"context"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/adapters/agent/claudecode"
	"tallpine.eu/tallpine/backend/internal/adapters/agent/codex"
	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/statefence"
)

func TestApplyFenceGitConfigAppendsAndGates(t *testing.T) {
	withFenceGOOS(t, "darwin")
	engine, _ := policytest.Managed(t, `{"schemaVersion":2,"rules":{"protectState":{"mode":"on"}}}`, nil)
	m := New(Deps{Agents: testAgentRegistry{domain.HarnessClaudeCode: claudecode.New()}, Store: newFakeStore()})
	m.SetAgentPolicy(engine)
	fenced := m.decide(context.Background(), "mer", domain.KindWorker, domain.HarnessClaudeCode, "s-1")
	env := map[string]string{"GIT_CONFIG_PARAMETERS": "'a.b'='c'"}
	applyFenceGitConfig(env, fenced, claudecode.New(), "")
	if want := "'a.b'='c' 'branch.autoSetupMerge'='false' 'push.default'='current'"; env["GIT_CONFIG_PARAMETERS"] != want || len(env) != 1 {
		t.Fatalf("env = %v", env)
	}
	plain := map[string]string{}
	applyFenceGitConfig(plain, agentpolicy.Decision{}, claudecode.New(), "")
	if len(plain) != 0 {
		t.Fatalf("unfenced env got keys: %v", plain)
	}
	// Codex is fenced only in bypass: a sandboxed launch keeps git defaults.
	if !statefence.Available("darwin") {
		return
	}
	sandboxed := map[string]string{}
	applyFenceGitConfig(sandboxed, fenced, codex.New(), domain.PermissionModeAuto)
	bypass := map[string]string{}
	applyFenceGitConfig(bypass, fenced, codex.New(), domain.PermissionModeBypassPermissions)
	byDefault := map[string]string{}
	applyFenceGitConfig(byDefault, fenced, codex.New(), domain.PermissionModeDefault)
	if len(sandboxed) != 0 || !hasFenceGitKeys(bypass) || !hasFenceGitKeys(byDefault) {
		t.Fatalf("codex auto = %v, bypass = %v, default = %v", sandboxed, bypass, byDefault)
	}
}

func hasFenceGitKeys(env map[string]string) bool {
	return strings.Contains(env["GIT_CONFIG_PARAMETERS"], "'push.default'='current'") && strings.Contains(env["GIT_CONFIG_PARAMETERS"], "'branch.autoSetupMerge'='false'")
}

// Every launch builds its env in runtimeEnvBase; walk spawn, restore and the
// shared builder that account move, chat and idle update call.
func TestFenceGitConfigOnEveryLaunchPath(t *testing.T) {
	withFenceGOOS(t, "darwin")
	m, st, rt := hardenedManager(t, fencingAgent{&recordingAgent{}})
	if _, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, RequestedMode: domain.SessionModeTUI}); err != nil {
		t.Fatal(err)
	}
	if !hasFenceGitKeys(rt.lastCfg.Env) {
		t.Fatalf("spawn env lacks keys: %v", rt.lastCfg.Env)
	}
	seedTerminal(st, "mer-9", domain.SessionMetadata{WorkspacePath: "/ws/mer-9", Branch: "b", AgentSessionID: "agent-x"})
	rec := st.sessions["mer-9"]
	rec.Harness = domain.HarnessClaudeCode
	st.sessions["mer-9"] = rec
	rt.lastCfg = ports.RuntimeConfig{}
	if _, err := m.RestoreWithMode(context.Background(), "mer-9"); err != nil {
		t.Fatal(err)
	}
	if !hasFenceGitKeys(rt.lastCfg.Env) {
		t.Fatalf("restore env lacks keys: %v", rt.lastCfg.Env)
	}
	for name, build := range map[string]func() (map[string]string, error){
		"account move/chat": func() (map[string]string, error) {
			return m.runtimeEnvChecked("mer-9", "mer", "", nil, "", "")
		},
		"idle update": func() (map[string]string, error) {
			env, _, err := m.launchRuntimeEnv("mer-9", "mer", "", nil)
			return env, err
		},
	} {
		env, err := build()
		if err != nil || !hasFenceGitKeys(env) {
			t.Fatalf("%s env = %v err = %v", name, env, err)
		}
	}
}
