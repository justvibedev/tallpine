package sessionmanager

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentlaunch"
	"tallpine.eu/tallpine/backend/internal/domain"
)

// TestAccountEnvClearedConfigDirIsUnsetNotEmpty: an empty CLAUDE_CONFIG_DIR
// makes Claude write its config into the worktree, so the device account's
// cleared variables must be removed from the launch, keeping earlier unsets.
func TestAccountEnvClearedConfigDirIsUnsetNotEmpty(t *testing.T) {
	m := &Manager{agentAccounts: clearedAccounts{}}
	env := map[string]string{"CLAUDE_CONFIG_DIR": "/project"}
	agentlaunch.SetUnset(env, []string{"GH_TOKEN"})
	if err := m.applyAgentAccountEnv("s", "", env, false); err != nil {
		t.Fatal(err)
	}
	if v, set := env["CLAUDE_CONFIG_DIR"]; set {
		t.Fatalf("CLAUDE_CONFIG_DIR must not be set, got %q", v)
	}
	got := map[string]bool{}
	for _, n := range agentlaunch.UnsetNames(env) {
		got[n] = true
	}
	if !got["CLAUDE_CONFIG_DIR"] || !got["GH_TOKEN"] || env["ANTHROPIC_API_KEY"] != "" {
		t.Fatalf("unexpected env %v", env)
	}
}

type clearedAccounts struct{ *transitionAccounts }

func (clearedAccounts) SessionLaunchEnv(_ context.Context, _ domain.SessionID) (map[string]string, error) {
	return map[string]string{"CLAUDE_CONFIG_DIR": "", "ANTHROPIC_API_KEY": ""}, nil
}

// TestEnvFilterAllowlistDoesNotReaddUnsetName: the allowlist keeps the daemon's
// CLAUDE_CONFIG_DIR, but a name the launch removes must stay out of the map.
func TestEnvFilterAllowlistDoesNotReaddUnsetName(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/daemon/claude")
	m := envFilterManager(t, allowlist(), &fakeGitHubTokens{})
	env := map[string]string{}
	agentlaunch.SetUnset(env, []string{"CLAUDE_CONFIG_DIR"})
	m.applyEnvFilter("mer-1", "mer", "", env, false)
	if v, ok := env["CLAUDE_CONFIG_DIR"]; ok {
		t.Fatalf("daemon CLAUDE_CONFIG_DIR re-added: %q", v)
	}
}
