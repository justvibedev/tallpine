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
		if tokens := domain.ResolveAutoCompactWindow(got.AutoCompactWindow, got.ModelContextWindow, 0); tokens != 400_000 {
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

func TestFreshLaunchOptionsFillsShellOutputLimit(t *testing.T) {
	t.Setenv(domain.EnvShellOutputLimit, "")
	m, _, _, _ := newManager()
	got := m.freshLaunchOptions(context.Background(), domain.HarnessClaudeCode, domain.KindWorker, "mer", domain.ProjectConfig{}, "", domain.AgentLaunchOptions{})
	if got.ShellOutputChars != domain.DefaultShellOutputLimit {
		t.Fatalf("default = %d", got.ShellOutputChars)
	}
	got = m.freshLaunchOptions(context.Background(), domain.HarnessCodex, domain.KindWorker, "mer", domain.ProjectConfig{Env: map[string]string{domain.EnvShellOutputLimit: "off"}}, "", domain.AgentLaunchOptions{})
	if got.ShellOutputChars != 0 {
		t.Fatalf("off = %d", got.ShellOutputChars)
	}
}

// A planner is a worker session with the planner role, so it compacts by the
// worker rule: the Settings default for its agent against a fixed 1M, never the
// orchestrator's 120k (B734).
func TestSpawnPlannerFollowsTheWorkerCompactionPath(t *testing.T) {
	t.Parallel()
	for _, role := range []domain.SessionRole{"", domain.SessionRolePlanner} {
		st := newFakeStore()
		st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
		agent := &recordingAgent{}
		m := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
			LookPath: func(string) (string, error) { return "/bin/true", nil }})
		m.defaults = compactDefaults{window: "40%"}
		m.SetModelCatalog(tuningCatalog{catalog: ports.AgentModelCatalog{Models: []ports.AgentModelInfo{
			{ID: "opus", IsDefault: true, ContextWindow: 1_000_000},
		}}})
		if _, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{
			ProjectID: "mer", Kind: domain.KindWorker, Role: role, Harness: domain.HarnessClaudeCode, RequestedMode: domain.SessionModeTUI,
			AgentConfig: ports.AgentConfig{Model: "opus"},
		}); err != nil {
			t.Fatalf("role %q: %v", role, err)
		}
		launch := agent.lastLaunch.Config.Launch
		if tokens := domain.ResolveAutoCompactWindow(launch.AutoCompactWindow, launch.ModelContextWindow, 0); launch.AutoCompactWindow != "40%" || tokens != 400_000 {
			t.Fatalf("role %q launch = %+v (%d tokens), want the 40%% default as 400000", role, launch, tokens)
		}
	}
}

// baselineStore is a fake store that also measures baselines.
type baselineStore struct {
	*fakeStore
	baseline int64
	asked    []domain.SessionKind
}

func (s *baselineStore) Baseline(_ context.Context, _ domain.ProjectID, kind domain.SessionKind, _ domain.AgentHarness) (int64, error) {
	s.asked = append(s.asked, kind)
	return s.baseline, nil
}

// A spawn reads the measured baseline at launch and hands it to the adapter,
// which floors the window at baseline + 100k (B735).
func TestSpawnCarriesTheMeasuredBaseline(t *testing.T) {
	t.Parallel()
	st := &baselineStore{fakeStore: newFakeStore(), baseline: 66_000}
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	agent := &recordingAgent{}
	m := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st.fakeStore},
		LookPath: func(string) (string, error) { return "/bin/true", nil }})
	m.defaults = compactDefaults{window: "120k"}
	m.SetModelCatalog(tuningCatalog{catalog: ports.AgentModelCatalog{Models: []ports.AgentModelInfo{
		{ID: "opus", IsDefault: true, ContextWindow: 1_000_000},
	}}})
	if _, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, RequestedMode: domain.SessionModeTUI,
		AgentConfig: ports.AgentConfig{Model: "opus"},
	}); err != nil {
		t.Fatal(err)
	}
	launch := agent.lastLaunch.Config.Launch
	if launch.AutoCompactBaseline != 66_000 || len(st.asked) != 1 || st.asked[0] != domain.KindWorker {
		t.Fatalf("launch = %+v asked %v, want baseline 66000 read once for a worker", launch, st.asked)
	}
	if tokens := domain.ResolveAutoCompactWindow(launch.AutoCompactWindow, launch.ModelContextWindow, launch.AutoCompactBaseline); tokens != 166_000 {
		t.Fatalf("resolved window = %d, want 166000", tokens)
	}
}

// The session readout resolves a terminal worker's window by the relaunch path.
func TestAutoCompactViewResolvesTheLaunchPath(t *testing.T) {
	t.Parallel()
	st := &baselineStore{fakeStore: newFakeStore(), baseline: 66_000}
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	m := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st.fakeStore},
		LookPath: func(string) (string, error) { return "/bin/true", nil }})
	m.defaults = compactDefaults{window: "120k"}
	m.SetModelCatalog(tuningCatalog{catalog: ports.AgentModelCatalog{Models: []ports.AgentModelInfo{{ID: "opus", IsDefault: true, ContextWindow: 1_000_000}}}})
	rec := domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode}
	view, ok := m.AutoCompactView(context.Background(), rec)
	if !ok || view.Baseline != 66_000 || view.Window != 166_000 || view.Trigger != 133_000 || view.ModelWindow != 1_000_000 || view.RoomTight {
		t.Fatalf("view = %+v ok=%v, want 66k baseline, 166k window, 133k trigger", view, ok)
	}
	rec.Harness = "kiro"
	if _, ok := m.AutoCompactView(context.Background(), rec); ok {
		t.Fatal("an agent Tallpine sets no window on has no readout")
	}
}

// An Auto choice passes no window, but a baseline that leaves too little room
// on the model still warns: the room check holds whatever the choice (B735).
func TestAutoCompactViewWarnsForAnAutoChoiceWithATightRoom(t *testing.T) {
	t.Parallel()
	st := &baselineStore{fakeStore: newFakeStore(), baseline: 150_000}
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	m := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st.fakeStore},
		LookPath: func(string) (string, error) { return "/bin/true", nil }})
	m.defaults = compactDefaults{window: ""}
	m.SetModelCatalog(tuningCatalog{catalog: ports.AgentModelCatalog{Models: []ports.AgentModelInfo{{ID: "sonnet", IsDefault: true, ContextWindow: 200_000}}}})
	rec := domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode}
	view, ok := m.AutoCompactView(context.Background(), rec)
	if !ok || view.Window != 0 || view.Baseline != 150_000 || view.ModelWindow != 200_000 || !view.RoomTight || view.RoomWarning() == "" {
		t.Fatalf("view = %+v ok=%v, want Auto (no window) with a tight-room warning", view, ok)
	}
}
