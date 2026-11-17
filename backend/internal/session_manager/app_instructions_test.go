package sessionmanager

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/skillbridge"
)

func appInstructionsManager(t *testing.T, project domain.ProjectConfig, app domain.AppInstructions, dataDir string) *Manager {
	t.Helper()
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: project}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: lookPath, DataDir: dataDir})
	m.defaults = appWorkflowDefaults{domain.WorkflowDefaults{AppInstructions: app}}
	return m
}

// App-wide rules come before the project's own, under their own heading, for
// workers and orchestrators; each role gets only its own app rules.
func TestSystemPromptPrependsAppRulesPerRole(t *testing.T) {
	ctx := context.Background()
	m := appInstructionsManager(t, domain.ProjectConfig{AgentRules: "Project worker rule.", OrchestratorRules: "Project orchestrator rule."},
		domain.AppInstructions{WorkerRules: "App worker rule.", OrchestratorRules: "App orchestrator rule.", ReviewerRules: "App reviewer rule."}, "")

	worker, err := m.buildSystemPrompt(ctx, domain.KindWorker, "mer", "mer-1", "", false)
	if err != nil {
		t.Fatal(err)
	}
	app := strings.Index(worker, "## App-Wide Rules\nApp worker rule.")
	project := strings.Index(worker, "## Project Rules\nProject worker rule.")
	if app < 0 || project < 0 || app > project {
		t.Fatalf("worker prompt: app rules at %d, project rules at %d:\n%s", app, project, worker)
	}
	if strings.Contains(worker, "App orchestrator rule.") || strings.Contains(worker, "App reviewer rule.") {
		t.Fatalf("worker got another role's app rules:\n%s", worker)
	}

	orch, err := m.buildSystemPrompt(ctx, domain.KindOrchestrator, "mer", "mer-2", "", false)
	if err != nil {
		t.Fatal(err)
	}
	app = strings.Index(orch, "## App-Wide Orchestrator Rules\nApp orchestrator rule.")
	project = strings.Index(orch, "## Project-Specific Orchestrator Rules\nProject orchestrator rule.")
	if app < 0 || project < 0 || app > project {
		t.Fatalf("orchestrator prompt: app rules at %d, project rules at %d:\n%s", app, project, orch)
	}
	if strings.Contains(orch, "App worker rule.") {
		t.Fatalf("orchestrator got the app worker rules:\n%s", orch)
	}
}

// App rules over their own cap, and app plus project rules over the combined
// cap, are cut with a marker naming the setting; the spawn still succeeds.
func TestSystemPromptCapsAppRulesPerLayerAndCombined(t *testing.T) {
	ctx := context.Background()
	big := strings.Repeat("a", domain.MaxAgentRulesBytes+100)
	m := appInstructionsManager(t, domain.ProjectConfig{AgentRules: strings.Repeat("p", domain.MaxAgentRulesBytes)}, domain.AppInstructions{WorkerRules: big}, "")
	worker, err := m.buildSystemPrompt(ctx, domain.KindWorker, "mer", "mer-1", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(worker, "shorten app.workerRules]") {
		t.Fatalf("app layer cap marker missing")
	}
	if strings.Contains(worker, strings.Repeat("a", domain.MaxAgentRulesBytes+1)) {
		t.Fatal("app rules over their own cap reached the prompt")
	}
	if !strings.Contains(worker, "[project rules truncated at") || strings.Contains(worker, strings.Repeat("p", domain.MaxAgentRulesBytes)) {
		t.Fatal("project rules were not cut to the combined cap")
	}
}

// The app skill switches apply where the project sets none; a project switch
// wins; and app custom skills reach every project's sessions.
func TestSystemPromptInheritsAppSkillSwitchesAndCustomSkills(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	store := skillbridge.CustomStore{Root: skillbridge.AppCustomDir(dataDir)}
	for _, n := range []string{"house-style", "app-off"} {
		if _, err := store.Create(n, "---\ndescription: about "+n+"\n---\n"); err != nil {
			t.Fatal(err)
		}
	}
	app := domain.AppInstructions{SkillRoles: domain.SkillRoles{"worker": {"design": false, "lint": false, "app-off": false}}}
	project := domain.ProjectConfig{SkillRoles: domain.SkillRoles{"worker": {"lint": true}}}
	m := appInstructionsManager(t, project, app, dataDir)

	worker, err := m.buildSystemPrompt(ctx, domain.KindWorker, "mer", "mer-1", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(worker, `- "design"`) || strings.Contains(worker, `- "lint"`) {
		t.Fatalf("off skills ignore the app layer or the project override:\n%s", worker)
	}
	want := `- house-style: "` + filepath.Join(store.Root, "house-style", "SKILL.md") + `"`
	if !strings.Contains(worker, want) || strings.Contains(worker, "- app-off:") {
		t.Fatalf("app custom skills missing or not switched:\n%s", worker)
	}

	skills, _ := roleSkillLaunch(skillbridge.Inventory{Skills: []skillbridge.Skill{{Name: "design", Scope: skillbridge.ScopeUser, Convention: skillbridge.ConvClaude}}},
		project, app.SkillRoles, domain.KindWorker, domain.HarnessClaudeCode)
	if got := (domain.AgentLaunchOptions{Skills: skills}).SkillLaunch().Off; !slices.Equal(got, []string{"design"}) {
		t.Fatalf("launch hides %v, want the app-switched-off design", got)
	}
}
