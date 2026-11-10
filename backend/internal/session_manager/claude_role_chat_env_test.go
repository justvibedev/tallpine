package sessionmanager

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// Chat sessions get their role's overrides in the launch env; other roles,
// other harnesses and a value smuggled in through project env get none.
func TestPinClaudeRoleSettings(t *testing.T) {
	cfg := domain.ProjectConfig{ClaudeAutoMemory: map[string]bool{"worker": false}, Env: map[string]string{domain.EnvShellOutputLimit: "off"}}
	env := map[string]string{}
	pinClaudeRoleSettings(env, domain.HarnessClaudeCode, domain.KindWorker, cfg)
	if env[domain.EnvClaudeRoleSettings] != `{"autoMemoryEnabled":false}` {
		t.Fatalf("worker env = %v", env)
	}
	env = map[string]string{domain.EnvClaudeRoleSettings: `{"x":1}`}
	pinClaudeRoleSettings(env, domain.HarnessClaudeCode, domain.KindOrchestrator, cfg)
	if _, ok := env[domain.EnvClaudeRoleSettings]; ok {
		t.Fatalf("orchestrator env = %v", env)
	}
	env = map[string]string{}
	pinClaudeRoleSettings(env, domain.HarnessCodex, domain.KindWorker, cfg)
	if len(env) != 0 {
		t.Fatalf("codex env = %v", env)
	}
}

// The Chat launch env Session Manager prepares carries the role overrides.
func TestPrepareChatControllerEnvPinsClaudeRoleSettings(t *testing.T) {
	m := envFilterManager(t, allowlist(), &fakeGitHubTokens{})
	rec := domain.SessionRecord{ID: "w-1", ProjectID: "mer", Harness: domain.HarnessClaudeCode, Kind: domain.KindWorker}
	m.store.(*fakeStore).sessions["w-1"] = rec
	cfg := domain.ProjectConfig{ClaudeAutoMemory: map[string]bool{"worker": false}, Env: map[string]string{domain.EnvShellOutputLimit: "off"}}
	_, env, err := m.prepareChatControllerEnv(context.Background(), rec, cfg, rec.ControllerOwner(), domain.HarnessClaudeCode)
	if err != nil {
		t.Fatal(err)
	}
	if env[domain.EnvClaudeRoleSettings] != `{"autoMemoryEnabled":false}` {
		t.Fatalf("chat env role settings = %q", env[domain.EnvClaudeRoleSettings])
	}
}

// The env a Chat session's driver receives at start (ChatStart.Env, which the
// claude driver builds session/new settings from) carries the project's role
// overrides, on a fresh spawn and on a restore. The launch-only env from
// PrepareControllerEnv is a different map and does not reach session/new.
func TestChatStartEnvCarriesClaudeRoleSettings(t *testing.T) {
	plugins := map[string]bool{"impeccable@impeccable": false}
	cfg := testRoleAgents()
	cfg.ClaudePlugins = map[string]map[string]bool{"orchestrator": plugins, "worker": plugins}
	cfg.ClaudeAutoMemory = map[string]bool{"worker": false}
	cfg.Env = map[string]string{domain.EnvShellOutputLimit: "off"}
	const workerWant = `{"autoMemoryEnabled":false,"enabledPlugins":{"impeccable@impeccable":false}}`
	const orchWant = `{"enabledPlugins":{"impeccable@impeccable":false}}`

	for _, tc := range []struct {
		name string
		kind domain.SessionKind
		want string
	}{{"worker", domain.KindWorker, workerWant}, {"orchestrator", domain.KindOrchestrator, orchWant}} {
		t.Run("spawn "+tc.name, func(t *testing.T) {
			launcher := &baseEnvLauncher{}
			mgr, store, _ := newChatManager(launcher)
			store.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: cfg}
			_, _, _, err := mgr.Spawn(context.Background(), ports.SpawnConfig{
				ProjectID: chatTestProject, Kind: tc.kind, Harness: domain.HarnessClaudeCode,
				Prompt: "go", RequestedMode: domain.SessionModeChat,
			})
			if err != nil {
				t.Fatal(err)
			}
			if got := launcher.baseEnvs[0][domain.EnvClaudeRoleSettings]; got != tc.want {
				t.Fatalf("spawn base chat env role settings = %q, want %q", got, tc.want)
			}
		})
	}

	// A chat that starts on another harness and later switches to Claude clones
	// this base env (service/chat providerSwitchTargetConfig), so it must hold
	// the settings already.
	t.Run("spawn codex worker, for a later switch to claude", func(t *testing.T) {
		launcher := &baseEnvLauncher{}
		mgr, store, _ := newChatManager(launcher)
		store.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: cfg}
		_, _, _, err := mgr.Spawn(context.Background(), ports.SpawnConfig{
			ProjectID: chatTestProject, Kind: domain.KindWorker, Harness: domain.HarnessCodex,
			Prompt: "go", RequestedMode: domain.SessionModeChat,
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := launcher.baseEnvs[0][domain.EnvClaudeRoleSettings]; got != workerWant {
			t.Fatalf("codex spawn base chat env role settings = %q, want %q", got, workerWant)
		}
	})

	t.Run("restore worker", func(t *testing.T) {
		launcher := &baseEnvLauncher{}
		mgr, store, _ := newChatManager(launcher)
		store.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: cfg}
		seedChatResumeSession(store, domain.ActivityExited)
		rec := store.sessions["mer-1"]
		rec.Harness = domain.HarnessClaudeCode
		store.sessions["mer-1"] = rec
		if _, err := mgr.ResumeAgentWithMode(context.Background(), rec.ID); err != nil {
			t.Fatal(err)
		}
		if got := launcher.baseEnvs[0][domain.EnvClaudeRoleSettings]; got != workerWant {
			t.Fatalf("restore base chat env role settings = %q, want %q", got, workerWant)
		}
	})
}

// baseEnvLauncher records the env Session Manager hands the chat service as
// ChatStart.Env, which the real service passes to the driver unchanged
// (service_start_provider.go); recordingLauncher swaps in the prepared env.
type baseEnvLauncher struct {
	recordingLauncher
	baseEnvs []map[string]string
}

func (l *baseEnvLauncher) StartChat(ctx context.Context, cfg ChatStart) (ChatStarted, error) {
	l.baseEnvs = append(l.baseEnvs, cfg.Env)
	return l.recordingLauncher.StartChat(ctx, cfg)
}

// Chat sessions get the shell output cap in their role settings, and the
// override turns it off.
func TestPinClaudeRoleSettingsCapsShellOutput(t *testing.T) {
	t.Setenv(domain.EnvShellOutputLimit, "")
	env := map[string]string{}
	pinClaudeRoleSettings(env, domain.HarnessClaudeCode, domain.KindOrchestrator, domain.ProjectConfig{})
	if env[domain.EnvClaudeRoleSettings] != `{"bashOutputMaxChars":12000}` {
		t.Fatalf("default env = %v", env)
	}
	cfg := domain.ProjectConfig{Env: map[string]string{domain.EnvShellOutputLimit: "20000"}}
	pinClaudeRoleSettings(env, domain.HarnessClaudeCode, domain.KindWorker, cfg)
	if env[domain.EnvClaudeRoleSettings] != `{"bashOutputMaxChars":20000}` {
		t.Fatalf("override env = %v", env)
	}
}
