package project_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/service/project"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

func roleDefaultsApp() domain.WorkflowDefaults {
	return domain.WorkflowDefaults{
		WorkerAgent: domain.HarnessClaudeCode, WorkerModel: "claude-sonnet-5-5", WorkerEffort: "medium",
		OrchestratorAgent: domain.HarnessClaudeCode, OrchestratorModel: "claude-opus-5-5", OrchestratorEffort: "medium",
	}
}

func addWithRoleDefaults(t *testing.T, app domain.WorkflowDefaults, cfg *domain.ProjectConfig) domain.ProjectConfig {
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
	m := project.NewWithDeps(project.Deps{Store: store, Logger: slog.New(slog.DiscardHandler)})
	if _, err := m.Add(ctx, project.AddInput{Path: gitRepo(t), ProjectID: ptr("np"), Config: cfg}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	row, _, _ := store.GetProject(ctx, "np")
	return row.Config
}

func TestAddAppliesRoleDefaultsToNewProject(t *testing.T) {
	got := addWithRoleDefaults(t, roleDefaultsApp(), nil)
	if got.Worker.Harness != domain.HarnessClaudeCode || got.Worker.AgentConfig.Model != "claude-sonnet-5-5" || got.Worker.AgentConfig.Effort != "medium" {
		t.Fatalf("worker = %+v", got.Worker)
	}
	if got.Orchestrator.Harness != domain.HarnessClaudeCode || got.Orchestrator.AgentConfig.Model != "claude-opus-5-5" || got.Orchestrator.AgentConfig.Effort != "medium" {
		t.Fatalf("orchestrator = %+v", got.Orchestrator)
	}
}

func TestAddExplicitRoleFieldsWinOverDefaults(t *testing.T) {
	// Same harness, own model: the model stays, empty fields fill.
	got := addWithRoleDefaults(t, roleDefaultsApp(), &domain.ProjectConfig{
		Worker: domain.RoleOverride{Harness: domain.HarnessClaudeCode, AgentConfig: domain.AgentConfig{Model: "mine", Effort: "high"}},
	})
	if got.Worker.AgentConfig.Model != "mine" || got.Worker.AgentConfig.Effort != "high" {
		t.Fatalf("worker = %+v, want the caller's model and effort kept", got.Worker)
	}
	if got.Orchestrator.AgentConfig.Model != "claude-opus-5-5" {
		t.Fatalf("orchestrator = %+v, want defaults for the role the caller left empty", got.Orchestrator)
	}
	// Another harness: the caller's choice stays whole; the claude model must not leak onto it.
	got = addWithRoleDefaults(t, roleDefaultsApp(), &domain.ProjectConfig{Worker: domain.RoleOverride{Harness: domain.HarnessCodex}})
	if got.Worker.Harness != domain.HarnessCodex || got.Worker.AgentConfig.Model != "" || got.Worker.AgentConfig.Effort != "" {
		t.Fatalf("worker = %+v, want codex with no inherited claude model", got.Worker)
	}
}

func TestAddEmptyRoleDefaultsChangeNothing(t *testing.T) {
	got := addWithRoleDefaults(t, domain.WorkflowDefaults{}, &domain.ProjectConfig{Worker: domain.RoleOverride{Harness: domain.HarnessCodex}})
	if got.Worker != (domain.RoleOverride{Harness: domain.HarnessCodex}) || got.Orchestrator != (domain.RoleOverride{}) {
		t.Fatalf("config changed with empty defaults: worker=%+v orchestrator=%+v", got.Worker, got.Orchestrator)
	}
}

func TestRoleDefaultsLeaveExistingProjectsUntouched(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	// Created while the built-in default is skipped, so it has no role config.
	m := project.NewWithDeps(project.Deps{Store: store, Logger: slog.New(slog.DiscardHandler), AgentNotReady: func(context.Context, domain.AgentHarness) bool { return true }})
	if _, err := m.Add(ctx, project.AddInput{Path: gitRepo(t), ProjectID: ptr("old")}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetWorkflowDefaults(ctx, roleDefaultsApp(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	// A later config write to the existing project must not pull defaults in either.
	if _, err := m.SetRules(ctx, "old", project.SetRulesInput{AgentRules: ptr("be careful")}); err != nil {
		t.Fatal(err)
	}
	row, _, _ := store.GetProject(ctx, "old")
	if row.Config.Worker != (domain.RoleOverride{}) || row.Config.Orchestrator != (domain.RoleOverride{}) {
		t.Fatalf("existing project got defaults: %+v", row.Config)
	}
}
