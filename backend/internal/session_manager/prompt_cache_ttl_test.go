package sessionmanager

import (
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestPinPromptCacheTTLPerRole(t *testing.T) {
	cases := []struct {
		name    string
		harness domain.AgentHarness
		kind    domain.SessionKind
		cfg     domain.ProjectConfig
		want    string
	}{
		{"artifact worker is not a reviewer", domain.HarnessClaudeCode, domain.KindWorker, domain.ProjectConfig{}, ""},
		{"impl worker", domain.HarnessClaudeCode, domain.KindWorker, domain.ProjectConfig{}, ""},
		{"orchestrator", domain.HarnessClaudeCode, domain.KindOrchestrator, domain.ProjectConfig{}, ""},
		{"codex artifact", domain.HarnessCodex, domain.KindWorker, domain.ProjectConfig{}, ""},
		{"configured worker", domain.HarnessClaudeCode, domain.KindWorker, domain.ProjectConfig{PromptCacheTTL: map[string]string{"worker": "1h"}}, "1h"},
	}
	for _, c := range cases {
		env := map[string]string{}
		pinPromptCacheTTL(env, c.harness, c.kind, c.cfg)
		if env[domain.EnvPromptCacheTTL] != c.want {
			t.Errorf("%s: got %q want %q", c.name, env[domain.EnvPromptCacheTTL], c.want)
		}
	}
}

func TestPinPromptCacheTTLHonorsInheritedEnv(t *testing.T) {
	cfg := domain.ProjectConfig{PromptCacheTTL: map[string]string{"worker": "1h"}}
	t.Setenv(domain.EnvPromptCacheTTL, "30m")
	env := map[string]string{}
	pinPromptCacheTTL(env, domain.HarnessClaudeCode, domain.KindWorker, cfg)
	if _, ok := env[domain.EnvPromptCacheTTL]; ok {
		t.Fatalf("inherited env must win, got %v", env)
	}
}
