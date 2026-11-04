package sessionmanager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func newRulesManager(t *testing.T, cfg domain.ProjectConfig, dir string) (*Manager, *recordingAgent) {
	t.Helper()
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: dir, Config: cfg}
	agent := &recordingAgent{}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	return New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: lookPath}), agent
}

func TestSpawnWorker_OverCapRulesFileTruncatedNotFailed(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	big := strings.Repeat("é rule line\n", domain.MaxAgentRulesBytes) // multi-byte runes, far over cap
	if err := os.WriteFile(filepath.Join(dir, "rules.md"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := testRoleAgents()
	cfg.AgentRulesFile = "rules.md"
	m, agent := newRulesManager(t, cfg, dir)

	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker}); err != nil {
		t.Fatalf("over-cap rules must not fail the spawn: %v", err)
	}
	sp := agent.lastLaunch.SystemPrompt
	if !strings.Contains(sp, "project rules truncated at") {
		t.Fatalf("system prompt lacks the truncation marker")
	}
	if strings.Count(sp, "é rule line") > domain.MaxAgentRulesBytes/len("é rule line\n")+1 {
		t.Fatalf("rules not cut to the cap")
	}
}

func TestCapProjectRulesKeepsValidUTF8(t *testing.T) {
	t.Parallel()

	got := capProjectRules(strings.Repeat("é", domain.MaxAgentRulesBytes))
	if !strings.HasSuffix(got, "agentRulesFile]") {
		t.Fatalf("missing marker: %q", got[len(got)-40:])
	}
	for _, r := range got {
		if r == '\uFFFD' {
			t.Fatal("cut split a rune")
		}
	}
}

func TestWorkerInstructionsReturnsEffectiveRules(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "rules.md"), []byte("File rule.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := testRoleAgents()
	cfg.AgentRules = "Inline rule."
	cfg.AgentRulesFile = "rules.md"
	m, _ := newRulesManager(t, cfg, dir)
	sess, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker})
	if err != nil {
		t.Fatal(err)
	}
	got, err := m.WorkerInstructions(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != "Inline rule.\n\nFile rule." {
		t.Fatalf("WorkerInstructions = %q", got)
	}
}
