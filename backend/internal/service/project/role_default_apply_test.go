package project_test

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/service/project"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

func roleApplyFixture(t *testing.T, app domain.WorkflowDefaults, projects map[string]domain.ProjectConfig) (*project.Service, *sqlite.Store) {
	t.Helper()
	ctx := context.Background()
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.SetWorkflowDefaults(ctx, app, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	for id, cfg := range projects {
		if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: id, Path: filepath.Join(t.TempDir(), id), DisplayName: id, RegisteredAt: time.Now().UTC(), Config: cfg}); err != nil {
			t.Fatal(err)
		}
	}
	return project.NewWithDeps(project.Deps{Store: store, DefaultHarness: domain.HarnessClaudeCode, Logger: slog.New(slog.DiscardHandler)}), store
}

func orchestratorApp() domain.WorkflowDefaults {
	return domain.WorkflowDefaults{
		OrchestratorAgent: domain.HarnessClaudeCode, OrchestratorModel: "claude-opus-5-5", OrchestratorEffort: "medium",
		ReviewerHarness: domain.ReviewerClaudeCode, ReviewerModel: "claude-opus-5-5", ReviewerEffort: "high",
	}
}

func orch(h domain.AgentHarness, model, effort string) domain.ProjectConfig {
	return domain.ProjectConfig{Orchestrator: domain.RoleOverride{Harness: h, AgentConfig: domain.AgentConfig{Model: model, Effort: effort}}}
}

func driftByID(r project.RoleDefaultDriftReport) map[domain.ProjectID]project.RoleDefaultDrift {
	out := map[domain.ProjectID]project.RoleDefaultDrift{}
	for _, d := range r.Projects {
		out[d.ProjectID] = d
	}
	return out
}

// B847: the drift report lists only projects whose orchestrator would not run
// with the app default, with what each runs today and how it would change.
func TestRoleDefaultDriftListsOnlyDifferingProjects(t *testing.T) {
	ctx := context.Background()
	m, _ := roleApplyFixture(t, orchestratorApp(), map[string]domain.ProjectConfig{
		"inherits":   {},
		"same":       orch(domain.HarnessClaudeCode, "claude-opus-5-5", "medium"),
		"high":       orch(domain.HarnessClaudeCode, "claude-opus-5-5", "high"),
		"effortonly": orch("", "", "low"),
		"codex":      orch(domain.HarnessCodex, "gpt-x", ""),
		"basewide":   {AgentConfig: domain.AgentConfig{Model: "haiku"}},
	})
	report, err := m.RoleDefaultDrift(ctx, domain.DefaultRoleOrchestrator)
	if err != nil {
		t.Fatal(err)
	}
	if want := (project.RoleValue{Agent: "claude-code", Model: "claude-opus-5-5", Effort: "medium"}); report.Default != want {
		t.Fatalf("default = %+v, want %+v", report.Default, want)
	}
	got := driftByID(report)
	want := map[domain.ProjectID]project.RoleDefaultDrift{
		"high":       {ProjectID: "high", Name: "high", Current: project.RoleValue{Agent: "claude-code", Model: "claude-opus-5-5", Effort: "high"}, Change: project.RoleDefaultClear},
		"effortonly": {ProjectID: "effortonly", Name: "effortonly", Current: project.RoleValue{Agent: "claude-code", Model: "claude-opus-5-5", Effort: "low"}, Change: project.RoleDefaultClear},
		"codex":      {ProjectID: "codex", Name: "codex", Current: project.RoleValue{Agent: "codex", Model: "gpt-x"}, Change: project.RoleDefaultClear},
		"basewide":   {ProjectID: "basewide", Name: "basewide", Current: project.RoleValue{Agent: "claude-code", Model: "haiku"}, Change: project.RoleDefaultSet},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("drift = %+v\nwant %+v", got, want)
	}
}

// B847: applying clears each drifting project's own orchestrator model and
// effort (switching a role on another agent to the default's agent), sets the
// default explicitly where a project-wide model would still win, and leaves
// every other field alone. Afterwards nothing drifts.
func TestApplyRoleDefaultBringsEveryProjectInLine(t *testing.T) {
	ctx := context.Background()
	m, store := roleApplyFixture(t, orchestratorApp(), map[string]domain.ProjectConfig{
		"high":     {AgentRules: "keep", Orchestrator: domain.RoleOverride{Harness: domain.HarnessClaudeCode, AgentConfig: domain.AgentConfig{Model: "claude-opus-5-5", Effort: "high", Permissions: "auto"}}},
		"codex":    {Orchestrator: domain.RoleOverride{Harness: domain.HarnessCodex, AgentConfig: domain.AgentConfig{Model: "gpt-x", Effort: "high", Mode: "high"}}},
		"basewide": {AgentConfig: domain.AgentConfig{Model: "haiku"}},
		"same":     orch(domain.HarnessClaudeCode, "claude-opus-5-5", "medium"),
	})
	res, err := m.ApplyRoleDefault(ctx, domain.DefaultRoleOrchestrator, project.ApplyRoleDefaultInput{})
	if err != nil {
		t.Fatal(err)
	}
	applied := append([]domain.ProjectID(nil), res.Applied...)
	slices.Sort(applied)
	if want := []domain.ProjectID{"basewide", "codex", "high"}; !reflect.DeepEqual(applied, want) || len(res.Failed) != 0 {
		t.Fatalf("applied = %v failed = %v, want %v", applied, res.Failed, want)
	}
	for id, want := range map[string]domain.ProjectConfig{
		"high":     {AgentRules: "keep", Orchestrator: domain.RoleOverride{Harness: domain.HarnessClaudeCode, AgentConfig: domain.AgentConfig{Permissions: "auto"}}},
		"codex":    orch(domain.HarnessClaudeCode, "", ""),
		"basewide": {AgentConfig: domain.AgentConfig{Model: "haiku"}, Orchestrator: domain.RoleOverride{AgentConfig: domain.AgentConfig{Model: "claude-opus-5-5", Effort: "medium"}}},
		"same":     orch(domain.HarnessClaudeCode, "claude-opus-5-5", "medium"),
	} {
		row, _, _ := store.GetProject(ctx, id)
		if !reflect.DeepEqual(row.Config, want) {
			t.Fatalf("%s config = %+v\nwant %+v", id, row.Config, want)
		}
	}
	report, err := m.RoleDefaultDrift(ctx, domain.DefaultRoleOrchestrator)
	if err != nil || len(report.Projects) != 0 {
		t.Fatalf("drift after apply = %+v, %v; want none", report.Projects, err)
	}
}

// B847: the apply touches only the projects the owner confirmed.
func TestApplyRoleDefaultOnlyListedProjects(t *testing.T) {
	ctx := context.Background()
	m, store := roleApplyFixture(t, orchestratorApp(), map[string]domain.ProjectConfig{
		"a": orch(domain.HarnessClaudeCode, "claude-opus-5-5", "high"),
		"b": orch(domain.HarnessClaudeCode, "claude-opus-5-5", "high"),
	})
	res, err := m.ApplyRoleDefault(ctx, domain.DefaultRoleOrchestrator, project.ApplyRoleDefaultInput{ProjectIDs: []domain.ProjectID{"a"}})
	if err != nil || !reflect.DeepEqual(res.Applied, []domain.ProjectID{"a"}) {
		t.Fatalf("applied = %v, %v", res.Applied, err)
	}
	if row, _, _ := store.GetProject(ctx, "b"); row.Config.Orchestrator.AgentConfig.Effort != "high" {
		t.Fatalf("unlisted project changed: %+v", row.Config.Orchestrator)
	}
}

// B847: a project's own reviewer on another agent drifts; one without a
// reviewer already follows the app default. Applying rewrites only the first
// reviewer and keeps the rest.
func TestApplyRoleDefaultReviewer(t *testing.T) {
	ctx := context.Background()
	m, store := roleApplyFixture(t, orchestratorApp(), map[string]domain.ProjectConfig{
		"none":    {},
		"inherit": {Reviewers: []domain.ReviewerConfig{{Harness: domain.ReviewerClaudeCode}}},
		"codex":   {Reviewers: []domain.ReviewerConfig{{Harness: domain.ReviewerCodex, AgentConfig: domain.AgentConfig{Model: "gpt-x"}}, {Harness: domain.ReviewerCodex}}},
	})
	report, err := m.RoleDefaultDrift(ctx, domain.DefaultRoleReviewer)
	if err != nil {
		t.Fatal(err)
	}
	if got := driftByID(report); len(got) != 1 || got["codex"].Current != (project.RoleValue{Agent: "codex", Model: "gpt-x"}) {
		t.Fatalf("reviewer drift = %+v", got)
	}
	if _, err := m.ApplyRoleDefault(ctx, domain.DefaultRoleReviewer, project.ApplyRoleDefaultInput{}); err != nil {
		t.Fatal(err)
	}
	row, _, _ := store.GetProject(ctx, "codex")
	want := []domain.ReviewerConfig{{Harness: domain.ReviewerClaudeCode}, {Harness: domain.ReviewerCodex}}
	if !reflect.DeepEqual(row.Config.Reviewers, want) {
		t.Fatalf("reviewers = %+v, want %+v", row.Config.Reviewers, want)
	}
}

func TestRoleDefaultDriftRefusesUnknownRoleOrMissingDefault(t *testing.T) {
	ctx := context.Background()
	m, _ := roleApplyFixture(t, domain.WorkflowDefaults{}, nil)
	for _, role := range []domain.DefaultRole{"planner", domain.DefaultRoleWorker} {
		_, err := m.RoleDefaultDrift(ctx, role)
		var apiErr *apierr.Error
		if !errors.As(err, &apiErr) || apiErr.Kind != apierr.KindInvalid {
			t.Fatalf("%s: err = %v, want invalid", role, err)
		}
	}
}

// A role's mode belongs to its agent: switching the agent clears it, and
// re-sending the same agent keeps it.
func TestSetRoleSettingsClearsModeOnlyWhenTheAgentChanges(t *testing.T) {
	ctx := context.Background()
	withMode := domain.ProjectConfig{Orchestrator: domain.RoleOverride{Harness: domain.HarnessCodex, AgentConfig: domain.AgentConfig{Mode: "high"}}}
	m, store := roleApplyFixture(t, domain.WorkflowDefaults{}, map[string]domain.ProjectConfig{"same": withMode, "switch": withMode})
	for id, agent := range map[string]string{"same": "codex", "switch": "claude-code"} {
		if _, err := m.SetRoleSettings(ctx, domain.ProjectID(id), project.SetRoleSettingsInput{Orchestrator: &project.RoleSettingsPatch{Agent: &agent}}); err != nil {
			t.Fatal(err)
		}
	}
	if row, _, _ := store.GetProject(ctx, "same"); row.Config.Orchestrator.AgentConfig.Mode != "high" {
		t.Fatalf("same agent dropped the mode: %+v", row.Config.Orchestrator)
	}
	if row, _, _ := store.GetProject(ctx, "switch"); row.Config.Orchestrator.AgentConfig.Mode != "" {
		t.Fatalf("switched agent kept the old mode: %+v", row.Config.Orchestrator)
	}
}
