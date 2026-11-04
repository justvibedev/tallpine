package sessionmanager

import (
	"context"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func role(h domain.AgentHarness, model string) domain.RoleOverride {
	return domain.RoleOverride{Harness: h, AgentConfig: domain.AgentConfig{Model: model}}
}

func TestPlannerModel_InheritsOrchestratorThenWorker(t *testing.T) {
	planner := domain.SessionRolePlanner
	orchestrator := role(domain.HarnessCodex, "orch-model")
	worker := role(domain.HarnessClaudeCode, "worker-model")
	cases := []struct {
		name        string
		cfg         domain.ProjectConfig
		wantHarness domain.AgentHarness
		wantModel   string
	}{
		{"else the orchestrator", domain.ProjectConfig{Orchestrator: orchestrator, Worker: worker}, domain.HarnessCodex, "orch-model"},
		{"else the worker", domain.ProjectConfig{Worker: worker}, domain.HarnessClaudeCode, "worker-model"},
		{"nothing configured stays empty (no fixed vendor)", domain.ProjectConfig{}, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := effectiveHarnessFor("", domain.KindWorker, planner, tc.cfg); got != tc.wantHarness {
				t.Errorf("harness = %q, want %q", got, tc.wantHarness)
			}
			if got := effectiveAgentConfigFor(tc.wantHarness, domain.KindWorker, planner, tc.cfg).Model; got != tc.wantModel {
				t.Errorf("model = %q, want %q", got, tc.wantModel)
			}
		})
	}
	// An ordinary worker never takes the orchestrator's or planner's choice.
	cfg := domain.ProjectConfig{Orchestrator: orchestrator, Worker: worker}
	if got := effectiveHarnessFor("", domain.KindWorker, "", cfg); got != domain.HarnessClaudeCode {
		t.Errorf("ordinary worker harness = %q, want its own", got)
	}
}

func TestPlannerModel_DoesNotInheritOrchestratorPermissions(t *testing.T) {
	orch := role(domain.HarnessClaudeCode, "m")
	orch.AgentConfig.Permissions = domain.PermissionModeBypassPermissions
	cfg := domain.ProjectConfig{Orchestrator: orch, AgentConfig: domain.AgentConfig{Permissions: domain.PermissionModeDefault}}
	got := effectiveAgentConfigFor(domain.HarnessClaudeCode, domain.KindWorker, domain.SessionRolePlanner, cfg)
	if got.Permissions != domain.PermissionModeDefault {
		t.Fatalf("planner permissions = %q; the orchestrator's bypass must not carry over", got.Permissions)
	}
}

func TestPlannerModel_PolicyLockWins(t *testing.T) {
	t.Cleanup(func() { SetPlannerLock(nil) })
	SetPlannerLock(func() (domain.RoleOverride, bool) { return role(domain.HarnessCodex, "locked"), true })
	cfg := domain.ProjectConfig{Orchestrator: role(domain.HarnessClaudeCode, "mine")}
	if got := effectiveHarnessFor("", domain.KindWorker, domain.SessionRolePlanner, cfg); got != domain.HarnessCodex {
		t.Fatalf("harness = %q, want the locked one", got)
	}
}

// B188 lives in the review engine's reviewer choice. The planner must not read
// the project's reviewer list: reviewers configured to something else leave the
// planner's model alone.
func TestPlannerModel_NotFromReviewerConfig(t *testing.T) {
	cfg := domain.ProjectConfig{
		Worker:    role(domain.HarnessClaudeCode, "worker-model"),
		Reviewers: []domain.ReviewerConfig{{Harness: domain.ReviewerHarness("codex")}},
	}
	if got := effectiveHarnessFor("", domain.KindWorker, domain.SessionRolePlanner, cfg); got != domain.HarnessClaudeCode {
		t.Fatalf("planner harness = %q, want the worker's, whatever the reviewers are", got)
	}
}

func TestPlannerPrompt_HasSelfCheck(t *testing.T) {
	for _, want := range []string{
		"PLANNER, not an implementer", "Settle what the code answers", "at most 5", "At most 20 assumptions; if over, merge related ones",
		"every requirement maps to a PR", "names and signatures stay consistent across PRs", "a plan much longer than the spec is a transcript, not a plan",
		"Consumes (field consumes)", "Produces (field produces)", "at most 5 strings", "no step-by-step TDD scripts and no code", "tallpine plan questions", "tallpine plan submit",
	} {
		if !strings.Contains(plannerPrompt, want) {
			t.Errorf("planner prompt lacks %q", want)
		}
	}
	if !strings.Contains(plannerPrompt, "Reporting done does not close the backlog item") {
		t.Error("planner prompt must say a done report leaves the item open")
	}
}

func TestPlannerOrchestratorPrompt_RulesAndMode(t *testing.T) {
	for _, want := range []string{"tallpine plan start", "--relayed", "--owner-message", "Never approve on your own", "do not paste plans"} {
		if !strings.Contains(plannerOrchestratorPrompt, want) {
			t.Errorf("orchestrator planner section lacks %q", want)
		}
	}
	if got := plannerModeRule(""); !strings.HasPrefix(got, "auto") {
		t.Errorf("empty mode = %q, want auto", got)
	}
	if got := plannerModeRule(domain.PlanningAlways); !strings.HasPrefix(got, "always") {
		t.Errorf("always = %q", got)
	}
	if got := plannerModeRule(domain.PlanningOff); !strings.HasPrefix(got, "off") {
		t.Errorf("off = %q", got)
	}
}

func TestPlannerModel_ProjectPlannerOverridesOrchestrator(t *testing.T) {
	cfg := domain.ProjectConfig{
		Orchestrator: role(domain.HarnessClaudeCode, "orch"),
		Planner:      role(domain.HarnessCodex, "plan"),
	}
	if got := effectiveHarnessFor("", domain.KindWorker, domain.SessionRolePlanner, cfg); got != domain.HarnessCodex {
		t.Fatalf("harness = %q, want the project's planner", got)
	}
	if got := effectiveAgentConfigFor(domain.HarnessCodex, domain.KindWorker, domain.SessionRolePlanner, cfg).Model; got != "plan" {
		t.Fatalf("model = %q, want plan", got)
	}
	SetPlannerLock(func() (domain.RoleOverride, bool) { return role(domain.HarnessClaudeCode, "locked"), true })
	t.Cleanup(func() { SetPlannerLock(nil) })
	if got := effectiveHarnessFor("", domain.KindWorker, domain.SessionRolePlanner, cfg); got != domain.HarnessClaudeCode {
		t.Fatalf("harness = %q, want the policy lock over the project's planner", got)
	}
}

func TestPlannerModeRule_FollowsProjectMode(t *testing.T) {
	m := &Manager{}
	for mode, want := range map[domain.PlanningMode]domain.PlanningMode{"": domain.PlanningAuto, domain.PlanningAlways: domain.PlanningAlways, domain.PlanningOff: domain.PlanningOff} {
		if got := m.planningModeFor(context.Background(), domain.ProjectConfig{PlanningMode: mode}); got != want {
			t.Errorf("planningModeFor(%q) = %q, want %q", mode, got, want)
		}
	}
}

// The prompt's planning mode follows the shared resolver: a project's own value
// (explicit auto included) beats the app default, which beats the built-in auto.
func TestPlanningModeFor_AppDefault(t *testing.T) {
	m := &Manager{defaults: appWorkflowDefaults{d: domain.WorkflowDefaults{PlanningMode: domain.PlanningAlways}}}
	for project, want := range map[domain.PlanningMode]domain.PlanningMode{"": domain.PlanningAlways, domain.PlanningAuto: domain.PlanningAuto, domain.PlanningOff: domain.PlanningOff} {
		if got := m.planningModeFor(context.Background(), domain.ProjectConfig{PlanningMode: project}); got != want {
			t.Errorf("planningModeFor(%q) = %q, want %q", project, got, want)
		}
	}
}
