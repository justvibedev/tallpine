package sessionmanager

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// appRoleDefaultsFixture is an owner-chosen app default per role (B847).
func appRoleDefaultsFixture() domain.WorkflowDefaults {
	return domain.WorkflowDefaults{
		OrchestratorAgent: domain.HarnessClaudeCode, OrchestratorModel: "claude-opus-x", OrchestratorEffort: "medium",
		WorkerAgent: domain.HarnessClaudeCode, WorkerModel: "claude-fable-x", WorkerEffort: "high",
	}
}

func TestLaunchAgentConfigInheritsAppRoleDefault(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		harness    domain.AgentHarness
		kind       domain.SessionKind
		role       domain.SessionRole
		cfg        domain.ProjectConfig
		noProvider bool
		noBuiltin  bool
		app        *domain.WorkflowDefaults
		wantModel  string
		wantEffort string
	}{
		{name: "empty orchestrator inherits model and effort", harness: domain.HarnessClaudeCode, kind: domain.KindOrchestrator, wantModel: "claude-opus-x", wantEffort: "medium"},
		{name: "pinned orchestrator agent without model inherits", harness: domain.HarnessClaudeCode, kind: domain.KindOrchestrator,
			cfg: domain.ProjectConfig{Orchestrator: domain.RoleOverride{Harness: domain.HarnessClaudeCode}}, wantModel: "claude-opus-x", wantEffort: "medium"},
		{name: "own effort kept, model inherited", harness: domain.HarnessClaudeCode, kind: domain.KindOrchestrator,
			cfg: domain.ProjectConfig{Orchestrator: domain.RoleOverride{AgentConfig: domain.AgentConfig{Effort: "high"}}}, wantModel: "claude-opus-x", wantEffort: "high"},
		{name: "own model keeps its own (empty) effort", harness: domain.HarnessClaudeCode, kind: domain.KindOrchestrator,
			cfg: domain.ProjectConfig{Orchestrator: domain.RoleOverride{AgentConfig: domain.AgentConfig{Model: "sonnet"}}}, wantModel: "sonnet"},
		{name: "project-wide model wins over the app default", harness: domain.HarnessClaudeCode, kind: domain.KindOrchestrator,
			cfg: domain.ProjectConfig{AgentConfig: domain.AgentConfig{Model: "haiku"}}, wantModel: "haiku"},
		{name: "another agent inherits nothing", harness: domain.HarnessCodex, kind: domain.KindOrchestrator},
		{name: "worker inherits the worker default", harness: domain.HarnessClaudeCode, kind: domain.KindWorker, wantModel: "claude-fable-x", wantEffort: "high"},
		{name: "planner follows the orchestrator default", harness: domain.HarnessClaudeCode, kind: domain.KindWorker, role: domain.SessionRolePlanner, wantModel: "claude-opus-x", wantEffort: "medium"},
		{name: "no defaults provider inherits nothing", harness: domain.HarnessClaudeCode, kind: domain.KindOrchestrator, noProvider: true},
		{name: "hermetic daemon drops built-in models", harness: domain.HarnessClaudeCode, kind: domain.KindOrchestrator, noBuiltin: true,
			app: func() *domain.WorkflowDefaults { d := domain.DefaultWorkflowDefaults(); return &d }()},
		{name: "built-in default applies otherwise", harness: domain.HarnessClaudeCode, kind: domain.KindOrchestrator,
			app:       func() *domain.WorkflowDefaults { d := domain.DefaultWorkflowDefaults(); return &d }(),
			wantModel: domain.BuiltinOrchestratorModel, wantEffort: domain.BuiltinRoleEffort},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := &Manager{noBuiltinRoleModels: tc.noBuiltin}
			if !tc.noProvider {
				app := appRoleDefaultsFixture()
				if tc.app != nil {
					app = *tc.app
				}
				m.defaults = appWorkflowDefaults{app}
			}
			got := m.launchAgentConfig(context.Background(), tc.harness, tc.kind, tc.role, tc.cfg)
			if got.Model != tc.wantModel || got.Effort != tc.wantEffort {
				t.Fatalf("model/effort = %q/%q, want %q/%q", got.Model, got.Effort, tc.wantModel, tc.wantEffort)
			}
		})
	}
}

// TestSpawn_InheritsAppRoleModel drives the real spawn path: an existing
// project with no orchestrator or worker model starts its sessions on the app
// default, and a project-set model still wins.
func TestSpawn_InheritsAppRoleModel(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		kind       domain.SessionKind
		own        domain.AgentConfig
		wantModel  string
		wantEffort string
	}{
		{"orchestrator inherits", domain.KindOrchestrator, domain.AgentConfig{}, "claude-opus-x", "medium"},
		{"worker inherits", domain.KindWorker, domain.AgentConfig{}, "claude-fable-x", "high"},
		{"own model wins", domain.KindWorker, domain.AgentConfig{Model: "claude-opus-x", Effort: "low"}, "claude-opus-x", "low"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m, st, _, _ := newManager()
			m.defaults = appWorkflowDefaults{appRoleDefaultsFixture()}
			m.modelCatalog = tuningCatalog{catalog: ports.AgentModelCatalog{Models: []ports.AgentModelInfo{
				{ID: "claude-opus-x", Efforts: []string{"low", "medium", "high"}},
				{ID: "claude-fable-x", Efforts: []string{"low", "medium", "high"}},
			}}}
			cfg := testRoleAgents()
			cfg.Worker.AgentConfig = tc.own
			cfg.Orchestrator.AgentConfig = tc.own
			st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/repo/mer", Kind: domain.ProjectKindWorkspace, Config: cfg}
			s, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: tc.kind})
			if err != nil {
				t.Fatal(err)
			}
			if got := st.sessions[s.ID].Metadata; got.Model != tc.wantModel || got.Effort != tc.wantEffort {
				t.Fatalf("session model/effort = %q/%q, want %q/%q", got.Model, got.Effort, tc.wantModel, tc.wantEffort)
			}
		})
	}
}
