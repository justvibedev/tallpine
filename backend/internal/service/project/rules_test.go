package project_test

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/service/project"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

// B189: SetRules changes only the two rules fields, keeps every other config
// field as stored, and logs who changed which field with old/new lengths.
func TestSetRulesChangesOnlyRulesAndLogs(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	logs := &bytes.Buffer{}
	m := project.NewWithDeps(project.Deps{Store: store, Logger: slog.New(slog.NewTextHandler(logs, nil))})
	cfg := domain.ProjectConfig{
		Env:               map[string]string{"K": "V"},
		PostCreate:        []string{"make setup"},
		Symlinks:          []string{".env"},
		AgentRules:        "old",
		OrchestratorRules: "keep me",
		AgentConfig:       domain.AgentConfig{Model: "m1"},
	}
	if err := store.UpsertProject(ctx, domain.ProjectRecord{
		ID: "app", Path: filepath.Join(t.TempDir(), "app"), DisplayName: "App",
		RegisteredAt: time.Now().UTC(), Config: cfg,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	rules := "new worker rules"
	if _, err := m.SetRules(ctx, "app", project.SetRulesInput{AgentRules: &rules, Actor: "orchestrator:app-orch"}); err != nil {
		t.Fatalf("SetRules: %v", err)
	}
	row, _, _ := store.GetProject(ctx, "app")
	want := cfg
	want.AgentRules = rules
	if !reflect.DeepEqual(row.Config, want) {
		t.Fatalf("config = %+v, want %+v", row.Config, want)
	}
	line := logs.String()
	for _, part := range []string{"actor=orchestrator:app-orch", "field=agentRules", "oldLength=3", "newLength=16"} {
		if !strings.Contains(line, part) {
			t.Errorf("log %q lacks %q", line, part)
		}
	}
	if strings.Contains(line, "orchestratorRules") {
		t.Errorf("unchanged field logged: %q", line)
	}

	_, err = m.SetRules(ctx, "app", project.SetRulesInput{})
	wantCode(t, err, "INVALID_PROJECT_RULES")
	_, err = m.SetRules(ctx, "missing", project.SetRulesInput{AgentRules: &rules})
	wantCode(t, err, "PROJECT_NOT_FOUND")
}

// Review of #494: orchestratorRules is capped on the rules path; a stored
// oversized value may still shrink.
func TestSetRulesCapsOrchestratorRules(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	m := project.NewWithDeps(project.Deps{Store: store})
	legacy := strings.Repeat("x", project.MaxOrchestratorRulesBytes+100)
	if err := store.UpsertProject(ctx, domain.ProjectRecord{
		ID: "app", Path: filepath.Join(t.TempDir(), "app"), DisplayName: "App",
		RegisteredAt: time.Now().UTC(), Config: domain.ProjectConfig{OrchestratorRules: legacy},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	grown := legacy + "more"
	_, err = m.SetRules(ctx, "app", project.SetRulesInput{OrchestratorRules: &grown})
	wantCode(t, err, "INVALID_PROJECT_CONFIG")
	shorter := legacy[:len(legacy)-50]
	if _, err := m.SetRules(ctx, "app", project.SetRulesInput{OrchestratorRules: &shorter}); err != nil {
		t.Fatalf("shrinking a legacy value: %v", err)
	}
	empty := ""
	if _, err := m.SetRules(ctx, "app", project.SetRulesInput{OrchestratorRules: &empty}); err != nil {
		t.Fatal(err)
	}
	over := strings.Repeat("y", project.MaxOrchestratorRulesBytes+1)
	_, err = m.SetRules(ctx, "app", project.SetRulesInput{OrchestratorRules: &over})
	wantCode(t, err, "INVALID_PROJECT_CONFIG")
	at := strings.Repeat("y", project.MaxOrchestratorRulesBytes)
	if _, err := m.SetRules(ctx, "app", project.SetRulesInput{OrchestratorRules: &at}); err != nil {
		t.Fatalf("at cap: %v", err)
	}
}

// The rules route sets orchestrator.autoCompactWindow (B193), keeps the
// orchestrator's other overrides, and refuses a value Validate rejects.
func TestSetRulesSetsOrchestratorCompactWindow(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	m := project.NewWithDeps(project.Deps{Store: store, Logger: slog.New(slog.DiscardHandler)})
	if err := store.UpsertProject(ctx, domain.ProjectRecord{
		ID: "app", Path: filepath.Join(t.TempDir(), "app"), DisplayName: "App", RegisteredAt: time.Now().UTC(),
		Config: domain.ProjectConfig{Orchestrator: domain.RoleOverride{Harness: domain.HarnessCodex}},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// B398: the change must reach running orchestrators, after it is stored.
	var notified []domain.ProjectID
	var seen string
	m.SetConfigChangedHook(func(ctx context.Context, id domain.ProjectID) {
		notified = append(notified, id)
		row, _, _ := store.GetProject(ctx, string(id))
		seen = row.Config.Orchestrator.AutoCompactWindow
	})
	// A second subscriber (B516) must not replace the first.
	second := 0
	m.SetConfigChangedHook(func(context.Context, domain.ProjectID) { second++ })
	window := "1m"
	if _, err := m.SetRules(ctx, "app", project.SetRulesInput{OrchestratorAutoCompactWindow: &window}); err != nil {
		t.Fatalf("set window: %v", err)
	}
	if second != 1 {
		t.Fatalf("second config hook ran %d times, want 1", second)
	}
	if len(notified) != 1 || notified[0] != "app" || seen != "1m" {
		t.Fatalf("config hook = %v (saw window %q), want [app] after the write", notified, seen)
	}
	row, _, _ := store.GetProject(ctx, "app")
	if row.Config.Orchestrator.AutoCompactWindow != "1m" || row.Config.Orchestrator.Harness != domain.HarnessCodex {
		t.Fatalf("orchestrator = %+v, want window 1m beside codex", row.Config.Orchestrator)
	}
	bad := "150000"
	if _, err := m.SetRules(ctx, "app", project.SetRulesInput{OrchestratorAutoCompactWindow: &bad}); err == nil {
		t.Fatal("an invalid window was accepted")
	}
}

// The rules route sets orchestrator.idleCompactAt (B204) and refuses an
// invalid value.
func TestSetRulesSetsOrchestratorIdleCompactAt(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	m := project.NewWithDeps(project.Deps{Store: store, Logger: slog.New(slog.DiscardHandler)})
	if err := store.UpsertProject(ctx, domain.ProjectRecord{
		ID: "app", Path: filepath.Join(t.TempDir(), "app"), DisplayName: "App", RegisteredAt: time.Now().UTC(),
		Config: domain.ProjectConfig{Orchestrator: domain.RoleOverride{AutoCompactWindow: "1m"}},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	at := "40%"
	if _, err := m.SetRules(ctx, "app", project.SetRulesInput{OrchestratorIdleCompactAt: &at}); err != nil {
		t.Fatalf("set idleCompactAt: %v", err)
	}
	row, _, _ := store.GetProject(ctx, "app")
	if row.Config.Orchestrator.IdleCompactAt != "40%" || row.Config.Orchestrator.AutoCompactWindow != "1m" {
		t.Fatalf("orchestrator = %+v, want idleCompactAt 40%% beside window 1m", row.Config.Orchestrator)
	}
	bad := "5%"
	if _, err := m.SetRules(ctx, "app", project.SetRulesInput{OrchestratorIdleCompactAt: &bad}); err == nil {
		t.Fatal("an invalid idleCompactAt was accepted")
	}
}

// The rules route sets orchestrator.researchCompactWindow (B413), keeps the
// other orchestrator fields, and refuses an invalid value.
func TestSetRulesSetsOrchestratorResearchCompactWindow(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	m := project.NewWithDeps(project.Deps{Store: store, Logger: slog.New(slog.DiscardHandler)})
	if err := store.UpsertProject(ctx, domain.ProjectRecord{
		ID: "app", Path: filepath.Join(t.TempDir(), "app"), DisplayName: "App", RegisteredAt: time.Now().UTC(),
		Config: domain.ProjectConfig{Orchestrator: domain.RoleOverride{AutoCompactWindow: "1m"}},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	for _, v := range []string{"300k", "off"} {
		w := v
		if _, err := m.SetRules(ctx, "app", project.SetRulesInput{OrchestratorResearchCompactWindow: &w}); err != nil {
			t.Fatalf("set %q: %v", v, err)
		}
		row, _, _ := store.GetProject(ctx, "app")
		if row.Config.Orchestrator.ResearchCompactWindow != v || row.Config.Orchestrator.AutoCompactWindow != "1m" {
			t.Fatalf("orchestrator = %+v, want research window %q beside window 1m", row.Config.Orchestrator, v)
		}
	}
	bad := "huge"
	if _, err := m.SetRules(ctx, "app", project.SetRulesInput{OrchestratorResearchCompactWindow: &bad}); err == nil {
		t.Fatal("an invalid research window was accepted")
	}
}
