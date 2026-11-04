package sessionmanager

import (
	"context"
	"errors"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// compactDefaults is a SessionModeDefaults that also serves the Settings
// per-harness compaction default.
type compactDefaults struct{ window string }

func (compactDefaults) DefaultSessionMode(context.Context) domain.SessionMode {
	return domain.SessionModeTUI
}
func (d compactDefaults) AutoCompactDefault(context.Context, domain.AgentHarness) string {
	return d.window
}

// A fresh terminal spawn never reads stored launch options, so the Settings
// default and the catalog window must be applied on that path itself.
func TestSpawnTUIAppliesSettingsCompactDefaultAndCatalogWindow(t *testing.T) {
	t.Parallel()
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	agent := &recordingAgent{}
	m := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil }})
	m.defaults = compactDefaults{window: "70%"}
	m.SetModelCatalog(tuningCatalog{catalog: ports.AgentModelCatalog{Models: []ports.AgentModelInfo{
		{ID: "sonnet", IsDefault: true, ContextWindow: 200_000},
	}}})
	if _, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, RequestedMode: domain.SessionModeTUI,
		AgentConfig: ports.AgentConfig{Model: "sonnet"},
	}); err != nil {
		t.Fatal(err)
	}
	launch := agent.lastLaunch.Config.Launch
	if launch.AutoCompactWindow != "70%" || launch.ModelContextWindow != 200_000 {
		t.Fatalf("fresh spawn launch = %+v, want 70%% of a 200000 window", launch)
	}
}

func TestFreshLaunchOptionsKeepsOwnChoiceAndNeverGuessesAWindow(t *testing.T) {
	t.Parallel()
	m, _, _, _ := newManager()
	m.defaults = compactDefaults{window: "70%"}
	m.SetModelCatalog(tuningCatalog{catalog: ports.AgentModelCatalog{Models: []ports.AgentModelInfo{{ID: "custom-model", ContextWindow: 0}}}})
	got := m.freshLaunchOptions(context.Background(), domain.HarnessClaudeCode, domain.KindWorker, "mer", domain.ProjectConfig{}, "custom-model", domain.AgentLaunchOptions{AutoCompactWindow: "90%"})
	if got.AutoCompactWindow != "90%" || got.ModelContextWindow != 0 {
		t.Fatalf("own choice / unknown window = %+v", got)
	}
	got = m.freshLaunchOptions(context.Background(), domain.HarnessClaudeCode, domain.KindWorker, "mer", domain.ProjectConfig{}, "custom-model", domain.AgentLaunchOptions{})
	if got.AutoCompactWindow != "70%" || got.ModelContextWindow != 0 {
		t.Fatalf("default with unknown window = %+v", got)
	}
}

// A catalog that errors, or is absent, must not leave a family alias without a
// window: the percent still becomes tokens (40% of opus's 1M is 400000).
func TestFreshLaunchOptionsResolvesFamilyAliasWithoutCatalog(t *testing.T) {
	t.Parallel()
	for name, wire := range map[string]func(*Manager){
		"erroring catalog": func(m *Manager) { m.SetModelCatalog(tuningCatalog{err: errors.New("no catalog")}) },
		"no catalog":       func(*Manager) {},
	} {
		m, _, _, _ := newManager()
		m.defaults = compactDefaults{window: "40%"}
		wire(m)
		got := m.freshLaunchOptions(context.Background(), domain.HarnessClaudeCode, domain.KindWorker, "mer", domain.ProjectConfig{}, "opus", domain.AgentLaunchOptions{})
		if tokens := domain.AutoCompactTokenCount(got.AutoCompactWindow, got.ModelContextWindow); tokens != 400_000 {
			t.Errorf("%s: compact tokens = %d (%+v), want 400000", name, tokens, got)
		}
	}
}

// A terminal orchestrator with no window of its own launches with the built-in
// one, a worker keeps Auto, and an explicit project choice wins; the Settings
// default, sized for workers, does not shadow the built-in one (B398).
func TestFreshLaunchOptionsGivesOrchestratorsTheirDefault(t *testing.T) {
	t.Parallel()
	m, _, _, _ := newManager()
	ctx := context.Background()
	if got := m.freshLaunchOptions(ctx, domain.HarnessClaudeCode, domain.KindOrchestrator, "mer", domain.ProjectConfig{}, "opus", domain.AgentLaunchOptions{}); got.AutoCompactWindow != domain.OrchestratorAutoCompactDefault {
		t.Fatalf("orchestrator = %+v, want the built-in window", got)
	}
	if got := m.freshLaunchOptions(ctx, domain.HarnessCodex, domain.KindWorker, "mer", domain.ProjectConfig{}, "opus", domain.AgentLaunchOptions{}); got.AutoCompactWindow != "" {
		t.Fatalf("worker = %+v, want Auto", got)
	}
	project := domain.ProjectConfig{Orchestrator: domain.RoleOverride{AutoCompactWindow: "1m"}}
	if got := m.freshLaunchOptions(ctx, domain.HarnessCodex, domain.KindOrchestrator, "mer", project, "opus", domain.AgentLaunchOptions{}); got.AutoCompactWindow != "1m" {
		t.Fatalf("project window = %+v, want 1m", got)
	}
	m.defaults = compactDefaults{window: "70%"}
	if got := m.freshLaunchOptions(ctx, domain.HarnessClaudeCode, domain.KindOrchestrator, "mer", domain.ProjectConfig{}, "opus", domain.AgentLaunchOptions{}); got.AutoCompactWindow != domain.OrchestratorAutoCompactDefault {
		t.Fatalf("settings default = %+v, want the built-in window", got)
	}
}
