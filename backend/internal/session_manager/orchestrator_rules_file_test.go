package sessionmanager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// orchestratorRulesFile is read from the project checkout and appended to
// orchestratorRules in the orchestrator's system prompt; workers never get it.
func TestSpawnOrchestrator_RulesFileReachesOrchestratorPrompt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docs", "orch.md"), []byte("File orchestrator rule.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := testRoleAgents()
	cfg.OrchestratorRules = "Inline orchestrator rule."
	cfg.OrchestratorRulesFile = "docs/orch.md"
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: dir, Config: cfg}
	agent := &recordingAgent{}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: lookPath})

	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindOrchestrator}); err != nil {
		t.Fatal(err)
	}
	got := agent.lastLaunch.SystemPrompt
	want := "## Project-Specific Orchestrator Rules\nInline orchestrator rule.\n\nFile orchestrator rule."
	if !strings.Contains(got, want) {
		t.Fatalf("orchestrator rules file missing from system prompt:\n%s", got)
	}

	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(agent.lastLaunch.SystemPrompt, "File orchestrator rule.") {
		t.Fatalf("worker got the orchestrator rules file:\n%s", agent.lastLaunch.SystemPrompt)
	}
}

// A missing orchestratorRulesFile fails the spawn with the setting named, as a
// missing agentRulesFile does for workers.
func TestBuildProjectRules_OrchestratorKeyNamesSetting(t *testing.T) {
	_, _, err := buildProjectRules(projectRulesConfig{ProjectPath: t.TempDir(), AgentRulesFile: "missing.md", Key: "orchestratorRules"})
	if err == nil || !strings.Contains(err.Error(), "orchestratorRulesFile") {
		t.Fatalf("err = %v", err)
	}
}
