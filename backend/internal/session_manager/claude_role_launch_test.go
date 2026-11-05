package sessionmanager

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// Fresh launches pick the project's claudePlugins and claudeAutoMemory entry
// for their role; a project without them launches exactly as before.
func TestFreshLaunchOptionsAppliesClaudeRoleSettings(t *testing.T) {
	t.Parallel()
	m, _, _, _ := newManager()
	ctx := context.Background()
	project := domain.ProjectConfig{
		ClaudePlugins: map[string]map[string]bool{
			"orchestrator": {"impeccable@m": false},
			"worker":       {"frontend-design@m": true},
		},
		ClaudeAutoMemory: map[string]bool{"worker": false, "orchestrator": true},
	}
	orch := m.freshLaunchOptions(ctx, domain.HarnessClaudeCode, domain.KindOrchestrator, "p", project, "", domain.AgentLaunchOptions{})
	if orch.EnabledPlugins != `{"impeccable@m":false}` || orch.AutoMemoryOff {
		t.Fatalf("orchestrator = %+v", orch)
	}
	worker := m.freshLaunchOptions(ctx, domain.HarnessClaudeCode, domain.KindWorker, "p", project, "", domain.AgentLaunchOptions{})
	if worker.EnabledPlugins != `{"frontend-design@m":true}` || !worker.AutoMemoryOff {
		t.Fatalf("worker = %+v", worker)
	}
	plain := m.freshLaunchOptions(ctx, domain.HarnessClaudeCode, domain.KindWorker, "p", domain.ProjectConfig{}, "", domain.AgentLaunchOptions{})
	if plain.EnabledPlugins != "" || plain.AutoMemoryOff {
		t.Fatalf("unset project changed the launch: %+v", plain)
	}
}
