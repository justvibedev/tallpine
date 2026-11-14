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

func addOnFreshStore(t *testing.T, notReady func(context.Context, domain.AgentHarness) bool, preset *domain.WorkflowDefaults) domain.ProjectConfig {
	t.Helper()
	ctx := context.Background()
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if preset != nil {
		if err := store.SetWorkflowDefaults(ctx, *preset, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	m := project.NewWithDeps(project.Deps{Store: store, Logger: slog.New(slog.DiscardHandler), AgentNotReady: notReady})
	if _, err := m.Add(ctx, project.AddInput{Path: gitRepo(t), ProjectID: ptr("np")}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	row, _, _ := store.GetProject(ctx, "np")
	return row.Config
}

func TestAddUsesBuiltinRoleDefaultsWhenNothingIsSet(t *testing.T) {
	got := addOnFreshStore(t, nil, nil)
	if got.Worker.Harness != domain.HarnessClaudeCode || got.Worker.AgentConfig.Model != "claude-sonnet-5-5" || got.Worker.AgentConfig.Effort != "medium" {
		t.Fatalf("worker = %+v", got.Worker)
	}
	if got.Orchestrator.Harness != domain.HarnessClaudeCode || got.Orchestrator.AgentConfig.Model != "claude-opus-5-5" || got.Orchestrator.AgentConfig.Effort != "medium" {
		t.Fatalf("orchestrator = %+v", got.Orchestrator)
	}
}

func TestAddSkipsBuiltinRoleDefaultsWhenClaudeNotReady(t *testing.T) {
	got := addOnFreshStore(t, func(context.Context, domain.AgentHarness) bool { return true }, nil)
	if got.Worker.Harness != "" || got.Orchestrator.Harness != "" || got.Worker.AgentConfig.Model != "" {
		t.Fatalf("roles = %+v / %+v, want no default", got.Worker, got.Orchestrator)
	}
}

func TestAddExplicitClearBeatsBuiltinRoleDefaults(t *testing.T) {
	preset := domain.DefaultWorkflowDefaults()
	preset.WorkerAgent, preset.WorkerModel, preset.WorkerEffort = "", "", ""
	got := addOnFreshStore(t, nil, &preset)
	if got.Worker.Harness != "" || got.Worker.AgentConfig.Model != "" {
		t.Fatalf("worker = %+v, want the explicit clear to win", got.Worker)
	}
	if got.Orchestrator.Harness != domain.HarnessClaudeCode {
		t.Fatalf("orchestrator = %+v, want the built-in default", got.Orchestrator)
	}
}

func TestAddExplicitAgentIsNotSkippedWhenNotReady(t *testing.T) {
	preset := domain.DefaultWorkflowDefaults()
	preset.WorkerAgent, preset.WorkerModel, preset.WorkerEffort = domain.HarnessCodex, "gpt-x", ""
	got := addOnFreshStore(t, func(context.Context, domain.AgentHarness) bool { return true }, &preset)
	if got.Worker.Harness != domain.HarnessCodex {
		t.Fatalf("worker = %+v, want the explicit codex default", got.Worker)
	}
}

// newManagerWithoutClaude is a machine without Claude Code: the built-in role
// defaults are skipped, so legacy tests see an unconfigured project.
func newManagerWithoutClaude(store project.Store) project.Manager {
	return project.NewWithDeps(project.Deps{Store: store, AgentNotReady: func(context.Context, domain.AgentHarness) bool { return true }})
}
