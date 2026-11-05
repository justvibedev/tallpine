package sessionmanager

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// Chat sessions get their role's overrides in the launch env; other roles,
// other harnesses and a value smuggled in through project env get none.
func TestPinClaudeRoleSettings(t *testing.T) {
	cfg := domain.ProjectConfig{ClaudeAutoMemory: map[string]bool{"worker": false}}
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
	cfg := domain.ProjectConfig{ClaudeAutoMemory: map[string]bool{"worker": false}}
	_, env, err := m.prepareChatControllerEnv(context.Background(), rec, cfg, rec.ControllerOwner(), domain.HarnessClaudeCode)
	if err != nil {
		t.Fatal(err)
	}
	if env[domain.EnvClaudeRoleSettings] != `{"autoMemoryEnabled":false}` {
		t.Fatalf("chat env role settings = %q", env[domain.EnvClaudeRoleSettings])
	}
}
