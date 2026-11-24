package domain

import "testing"

func TestRoleModelDefaultFill(t *testing.T) {
	t.Parallel()
	def := RoleModelDefault{Agent: HarnessClaudeCode, Model: "opus", Effort: "medium"}
	for _, tc := range []struct {
		name   string
		launch AgentHarness
		in     AgentConfig
		want   AgentConfig
	}{
		{"empty inherits both", HarnessClaudeCode, AgentConfig{}, AgentConfig{Model: "opus", Effort: "medium"}},
		{"own effort kept", HarnessClaudeCode, AgentConfig{Effort: "high"}, AgentConfig{Model: "opus", Effort: "high"}},
		{"own model keeps own effort", HarnessClaudeCode, AgentConfig{Model: "sonnet"}, AgentConfig{Model: "sonnet"}},
		{"other agent untouched", HarnessCodex, AgentConfig{}, AgentConfig{}},
		{"permissions kept", HarnessClaudeCode, AgentConfig{Permissions: "auto"}, AgentConfig{Model: "opus", Effort: "medium", Permissions: "auto"}},
	} {
		if got := def.Fill(tc.launch, tc.in); got != tc.want {
			t.Errorf("%s: Fill = %+v, want %+v", tc.name, got, tc.want)
		}
	}
	if got := (RoleModelDefault{}).Fill(HarnessClaudeCode, AgentConfig{}); got != (AgentConfig{}) {
		t.Errorf("no default: Fill = %+v, want empty", got)
	}
}

// B847: a project reviewer on the default's agent with no model of its own
// runs with the app default reviewer model and effort.
func TestEffectiveReviewerInheritsAppModelAndEffort(t *testing.T) {
	t.Parallel()
	app := WorkflowDefaults{ReviewerHarness: ReviewerClaudeCode, ReviewerModel: "opus", ReviewerEffort: "high"}
	for _, tc := range []struct {
		name string
		cfg  ProjectConfig
		h    ReviewerHarness
		want AgentConfig
	}{
		{"no reviewer follows the app default", ProjectConfig{}, ReviewerClaudeCode, AgentConfig{Model: "opus", Effort: "high"}},
		{"own reviewer without model inherits", ProjectConfig{Reviewers: []ReviewerConfig{{Harness: ReviewerClaudeCode}}}, ReviewerClaudeCode, AgentConfig{Model: "opus", Effort: "high"}},
		{"own model wins", ProjectConfig{Reviewers: []ReviewerConfig{{Harness: ReviewerClaudeCode, AgentConfig: AgentConfig{Model: "sonnet"}}}}, ReviewerClaudeCode, AgentConfig{Model: "sonnet"}},
		{"other agent untouched", ProjectConfig{Reviewers: []ReviewerConfig{{Harness: ReviewerCodex}}}, ReviewerCodex, AgentConfig{}},
	} {
		h, cfg := tc.cfg.EffectiveReviewer(app, HarnessClaudeCode)
		if h != tc.h || cfg != tc.want {
			t.Errorf("%s: reviewer = %s %+v, want %s %+v", tc.name, h, cfg, tc.h, tc.want)
		}
	}
}

func TestWorkflowDefaultsWithoutBuiltinRoleModels(t *testing.T) {
	t.Parallel()
	d := DefaultWorkflowDefaults()
	d.ReviewerModel, d.ReviewerEffort = "custom", "high"
	got, changed := d.WithoutBuiltinRoleModels()
	if !changed || got.OrchestratorModel != "" || got.OrchestratorEffort != "" || got.WorkerModel != "" || got.WorkerEffort != "" {
		t.Fatalf("built-in models kept: %+v", got)
	}
	if got.ReviewerModel != "custom" || got.ReviewerEffort != "high" || got.OrchestratorAgent != BuiltinOrchestratorAgent {
		t.Fatalf("owner values or agents dropped: %+v", got)
	}
}
