package sessionmanager

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// B454: the Claude adapter builds the protectState fence from LaunchConfig.Env
// (it needs the run file to locate the state it denies). A fresh terminal spawn
// handed the adapter no Env, so a Security mode session launched unsandboxed
// ("protectState is on but the launch has no run file").
func TestSpawnTUIHandsTheAdapterTheLaunchEnvForTheFence(t *testing.T) {
	t.Parallel()
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	agent := &recordingAgent{}
	m := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil }, RunFilePath: "/state/running.json"})
	if _, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, RequestedMode: domain.SessionModeTUI,
	}); err != nil {
		t.Fatal(err)
	}
	if got := agent.lastLaunch.Env[EnvRunFile]; got != "/state/running.json" {
		t.Fatalf("fresh spawn LaunchConfig.Env[%s] = %q, want the run file (env %v)", EnvRunFile, got, agent.lastLaunch.Env)
	}
}

// The fresh-launch fallback of a relaunch must carry the env too.
func TestFreshLaunchArgvForwardsTheLaunchEnv(t *testing.T) {
	t.Parallel()
	agent := &recordingAgent{}
	env := map[string]string{EnvRunFile: "/state/running.json"}
	if _, _, _, err := freshLaunchArgv(context.Background(), agentpolicy.Decision{}, agent, "mer-1", t.TempDir(),
		domain.SessionMetadata{Prompt: "go"}, "", "", ports.AgentConfig{}, domain.KindWorker, t.TempDir(), false, "", env); err != nil {
		t.Fatal(err)
	}
	if got := agent.lastLaunch.Env[EnvRunFile]; got != "/state/running.json" {
		t.Fatalf("LaunchConfig.Env[%s] = %q, want the run file", EnvRunFile, got)
	}
}
