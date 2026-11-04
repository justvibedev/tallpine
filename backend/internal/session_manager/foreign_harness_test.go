package sessionmanager

import (
	"context"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestEffectiveAgentConfig_ForeignLaunchHarnessDropsProjectBase(t *testing.T) {
	cfg := domain.ProjectConfig{
		AgentConfig: domain.AgentConfig{Model: "claude-opus-4", Effort: "high", Mode: "plan", Permissions: domain.PermissionModeAuto},
		Worker:      domain.RoleOverride{Harness: domain.HarnessClaudeCode},
	}
	got := effectiveAgentConfig(domain.HarnessCodex, domain.KindWorker, cfg)
	if got.Model != "" || got.Effort != "" || got.Mode != "" || got.Permissions != domain.PermissionModeAuto {
		t.Fatalf("codex under claude-pinned role = %+v, want base model/effort/mode dropped, permissions kept", got)
	}
	if got := effectiveAgentConfig(domain.HarnessClaudeCode, domain.KindWorker, cfg); got.Model != "claude-opus-4" {
		t.Fatalf("matching harness lost its model: %+v", got)
	}
}

func TestEffectiveAgentConfig_UnpinnedRoleBaseDroppedOnForeignHarness(t *testing.T) {
	cfg := domain.ProjectConfig{AgentConfig: domain.AgentConfig{Model: "claude-opus-4", Effort: "high", Permissions: domain.PermissionModeAuto}}
	got := effectiveAgentConfig(domain.HarnessCodex, domain.KindWorker, cfg)
	if got.Model != "" || got.Effort != "" || got.Permissions != domain.PermissionModeAuto {
		t.Fatalf("codex under unpinned project = %+v, want base dropped, permissions kept", got)
	}
	if got := effectiveAgentConfig(domain.FallbackHarness, domain.KindWorker, cfg); got.Model != "claude-opus-4" {
		t.Fatalf("fallback harness lost its base model: %+v", got)
	}
}

// A project whose default agent is codex owns its base for codex launches.
func TestEffectiveAgentConfig_CodexDefaultProjectKeepsBaseForCodex(t *testing.T) {
	cfg := domain.ProjectConfig{AgentConfig: domain.AgentConfig{Model: "gpt-5", Effort: "high"}}
	if got := effectiveAgentConfigDefault(domain.HarnessCodex, domain.KindWorker, "", cfg, domain.HarnessCodex); got.Model != "gpt-5" || got.Effort != "high" {
		t.Fatalf("codex under codex-default project = %+v, want base kept", got)
	}
	if got := effectiveAgentConfigDefault(domain.HarnessClaudeCode, domain.KindWorker, "", cfg, domain.HarnessCodex); got.Model != "" {
		t.Fatalf("claude under codex-default project kept %q, want dropped", got.Model)
	}
}

// readyStub serves only cached snapshots; the Ensure path fails the test.
type readyStub struct {
	t     *testing.T
	ready map[string]bool
}

func (s readyStub) EnsureAgentReadiness(context.Context, string, domain.AgentReadinessPurpose) (domain.AgentReadinessSnapshot, error) {
	s.t.Error("prompt build must not call EnsureAgentReadiness")
	return domain.AgentReadinessSnapshot{}, nil
}

func (s readyStub) CachedAgentSnapshots() []domain.AgentReadinessSnapshot {
	var out []domain.AgentReadinessSnapshot
	for _, id := range []string{"claude-code", "codex"} {
		r := domain.AgentReadinessNotReady
		if s.ready[id] {
			r = domain.AgentReadinessReady
		}
		out = append(out, domain.AgentReadinessSnapshot{ID: id, EffectiveReadiness: r})
	}
	return out
}
func (readyStub) InvalidateAgentInstallation(string)   {}
func (readyStub) InvalidateAgentAuthentication(string) {}
func (readyStub) RecheckAgent(string)                  {}

func TestOrchestratorPromptListsReadyAgents(t *testing.T) {
	m := &Manager{}
	if got := m.readyAgents(); got != nil {
		t.Fatalf("no readiness provider: got %v, want nil", got)
	}
	m.SetAgentReadiness(readyStub{t: t, ready: map[string]bool{"codex": true}})
	agents := m.readyAgents()
	prompt := orchestratorSystemPrompt(promptProject{ID: "p", Name: "P", AvailableAgents: agents})
	if !strings.Contains(prompt, "Installed and signed in: codex.") || !strings.Contains(prompt, "tallpine agent ls") {
		t.Fatalf("prompt lacks the agent list")
	}
	if static := orchestratorSystemPrompt(promptProject{ID: "p", Name: "P"}); strings.Contains(static, "Installed and signed in") {
		t.Fatal("fallback prompt lists agents")
	}
}

func TestSpawnForeignHarnessLaunchesWithoutProjectClaudeModel(t *testing.T) {
	t.Parallel()
	launcher := &recordingLauncher{}
	mgr, store, _ := newChatManager(launcher)
	project := store.projects[string(chatTestProject)]
	project.Config.Worker.Harness = "" // unpinned project
	project.Config.AgentConfig.Model = "claude-opus-4"
	store.projects[string(chatTestProject)] = project

	if _, _, _, err := mgr.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID: chatTestProject, Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		RequestedMode: domain.SessionModeChat,
	}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if len(launcher.started) != 1 || launcher.started[0].Model != "" {
		t.Fatalf("started = %+v, want one launch without the project's Claude model", launcher.started)
	}
}
