package project_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/managedcfg"
	"tallpine.eu/tallpine/backend/internal/service/project"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

func sp(s string) *string { return &s }

// B643: SetRoleSettings changes only the three roles' agent/model/effort and
// keeps every other config field, including env and post-create.
func TestSetRoleSettingsMergesAndKeepsOtherFields(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	m := project.NewWithDeps(project.Deps{Store: store, Logger: slog.New(slog.DiscardHandler)})
	cfg := domain.ProjectConfig{
		Env:        map[string]string{"K": "V"},
		PostCreate: []string{"make setup"},
		Symlinks:   []string{".env"},
		AgentRules: "rules",
		Worker:     domain.RoleOverride{Harness: domain.HarnessCodex, AgentConfig: domain.AgentConfig{Model: "w1", Permissions: "auto"}},
		Reviewers: []domain.ReviewerConfig{
			{Harness: domain.ReviewerClaudeCode, AgentConfig: domain.AgentConfig{Model: "r1"}},
			{Harness: domain.ReviewerCodex},
		},
	}
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "app", Path: filepath.Join(t.TempDir(), "app"), DisplayName: "App", RegisteredAt: time.Now().UTC(), Config: cfg}); err != nil {
		t.Fatal(err)
	}
	_, err = m.SetRoleSettings(ctx, "app", project.SetRoleSettingsInput{
		Orchestrator: &project.RoleSettingsPatch{Agent: sp("codex"), Model: sp("o1"), Effort: sp("high")},
		Worker:       &project.RoleSettingsPatch{Effort: sp("low")},
		Reviewer:     &project.RoleSettingsPatch{Model: sp("r2")},
	})
	if err != nil {
		t.Fatalf("SetRoleSettings: %v", err)
	}
	row, _, _ := store.GetProject(ctx, "app")
	want := cfg
	want.Orchestrator = domain.RoleOverride{Harness: domain.HarnessCodex, AgentConfig: domain.AgentConfig{Model: "o1", Effort: "high"}}
	want.Worker.AgentConfig.Effort = "low"
	want.Reviewers = []domain.ReviewerConfig{
		{Harness: domain.ReviewerClaudeCode, AgentConfig: domain.AgentConfig{Model: "r2"}},
		{Harness: domain.ReviewerCodex},
	}
	if !reflect.DeepEqual(row.Config, want) {
		t.Fatalf("config = %+v\nwant     %+v", row.Config, want)
	}

	// Clearing a field and creating a first reviewer.
	if _, err := m.SetRoleSettings(ctx, "app", project.SetRoleSettingsInput{Worker: &project.RoleSettingsPatch{Model: sp("")}}); err != nil {
		t.Fatal(err)
	}
	row, _, _ = store.GetProject(ctx, "app")
	if row.Config.Worker.AgentConfig.Model != "" || row.Config.Worker.AgentConfig.Permissions != "auto" {
		t.Fatalf("worker = %+v", row.Config.Worker)
	}
}

func TestSetRoleSettingsRefusesBadInput(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	m := project.NewWithDeps(project.Deps{Store: store, Logger: slog.New(slog.DiscardHandler)})
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "app", Path: filepath.Join(t.TempDir(), "app"), RegisteredAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	for name, in := range map[string]project.SetRoleSettingsInput{
		"empty":        {},
		"empty role":   {Worker: &project.RoleSettingsPatch{}},
		"bad effort":   {Worker: &project.RoleSettingsPatch{Effort: sp("turbo")}},
		"bad harness":  {Orchestrator: &project.RoleSettingsPatch{Agent: sp("nope")}},
		"bad reviewer": {Reviewer: &project.RoleSettingsPatch{Agent: sp("nope")}},
	} {
		if _, err := m.SetRoleSettings(ctx, "app", in); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	if _, err := m.SetRoleSettings(ctx, "missing", project.SetRoleSettingsInput{Worker: &project.RoleSettingsPatch{Effort: sp("low")}}); err == nil {
		t.Error("unknown project: want error")
	}
	row, _, _ := store.GetProject(ctx, "app")
	if !reflect.DeepEqual(row.Config, domain.ProjectConfig{}) {
		t.Fatalf("a refused write changed config: %+v", row.Config)
	}
}

func openRoleStore(t *testing.T) *sqlite.Store {
	t.Helper()
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// A concurrent write to another config field must not be lost: the merge runs
// in one store transaction, not a read then a stale-row upsert.
func TestSetRoleSettingsDoesNotLoseConcurrentWrites(t *testing.T) {
	ctx := context.Background()
	store := openRoleStore(t)
	m := project.NewWithDeps(project.Deps{Store: store, Logger: slog.New(slog.DiscardHandler)})
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "app", Path: filepath.Join(t.TempDir(), "app"), RegisteredAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = m.SetRoleSettings(ctx, "app", project.SetRoleSettingsInput{Worker: &project.RoleSettingsPatch{Effort: sp("low")}})
		}()
		go func() {
			defer wg.Done()
			_, _, _ = store.SetProjectPermissions(ctx, "app", domain.PermissionModeDefault)
		}()
	}
	// A direct write racing the service's read-merge-write.
	if _, _, err := store.UpdateProjectConfig(ctx, "app", func(r domain.ProjectRecord) (domain.ProjectConfig, error) {
		c := r.Config
		c.AgentRules = "desktop rules"
		return c, nil
	}); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	row, _, _ := store.GetProject(ctx, "app")
	if row.Config.AgentRules != "desktop rules" || row.Config.Worker.AgentConfig.Effort != "low" {
		t.Fatalf("a concurrent write was lost: %+v", row.Config)
	}
}

func TestSetRoleSettingsHonoursManagedLocks(t *testing.T) {
	ctx := context.Background()
	store := openRoleStore(t)
	locks := managedcfg.New(func() map[string]json.RawMessage {
		return map[string]json.RawMessage{"project.worker": json.RawMessage(`{"agent":"codex"}`)}
	})
	store.SetManaged(locks)
	m := project.NewWithDeps(project.Deps{Store: store, Logger: slog.New(slog.DiscardHandler)})
	m.SetManaged(locks)
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "app", Path: filepath.Join(t.TempDir(), "app"), RegisteredAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	_, err := m.SetRoleSettings(ctx, "app", project.SetRoleSettingsInput{Worker: &project.RoleSettingsPatch{Effort: sp("low")}})
	var api *apierr.Error
	if !errors.As(err, &api) || api.Code != managedcfg.CodeLocked {
		t.Fatalf("err = %v, want SETTING_LOCKED", err)
	}
	if _, err := m.SetRoleSettings(ctx, "app", project.SetRoleSettingsInput{Orchestrator: &project.RoleSettingsPatch{Effort: sp("low")}}); err != nil {
		t.Fatalf("an unlocked role: %v", err)
	}
}

func TestSetRoleSettingsReviewerRules(t *testing.T) {
	ctx := context.Background()
	store := openRoleStore(t)
	m := project.NewWithDeps(project.Deps{Store: store, Logger: slog.New(slog.DiscardHandler)})
	seed := func(id string, kind domain.ProjectKind, cfg domain.ProjectConfig) {
		if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: id, Path: filepath.Join(t.TempDir(), id), Kind: kind, RegisteredAt: time.Now().UTC(), Config: cfg}); err != nil {
			t.Fatal(err)
		}
	}
	seed("scratch", domain.ProjectKindScratch, domain.ProjectConfig{Worker: domain.RoleOverride{Harness: domain.HarnessCodex}})
	if _, err := m.SetRoleSettings(ctx, "scratch", project.SetRoleSettingsInput{Reviewer: &project.RoleSettingsPatch{Effort: sp("low")}}); err == nil {
		t.Fatal("scratch project accepted a reviewer")
	}
	row, _, _ := store.GetProject(ctx, "scratch")
	if len(row.Config.Reviewers) != 0 {
		t.Fatalf("reviewers stored on scratch: %+v", row.Config.Reviewers)
	}

	// With no reviewer entry the reviewer follows the worker; creating one keeps that.
	seed("cx", "", domain.ProjectConfig{Worker: domain.RoleOverride{Harness: domain.HarnessCodex, AgentConfig: domain.AgentConfig{Model: "w1", Effort: "high"}}})
	if _, err := m.SetRoleSettings(ctx, "cx", project.SetRoleSettingsInput{Reviewer: &project.RoleSettingsPatch{Effort: sp("low")}}); err != nil {
		t.Fatal(err)
	}
	row, _, _ = store.GetProject(ctx, "cx")
	if len(row.Config.Reviewers) != 1 || row.Config.Reviewers[0].Harness != domain.ReviewerCodex || row.Config.Reviewers[0].AgentConfig.Effort != "low" || row.Config.Reviewers[0].AgentConfig.Model != "w1" {
		t.Fatalf("reviewers = %+v, want codex, inherited model w1, effort low", row.Config.Reviewers)
	}
	// Picking another agent starts empty: the worker's model does not apply to it.
	seed("cx2", "", domain.ProjectConfig{Worker: domain.RoleOverride{Harness: domain.HarnessCodex, AgentConfig: domain.AgentConfig{Model: "w1", Effort: "high"}}})
	if _, err := m.SetRoleSettings(ctx, "cx2", project.SetRoleSettingsInput{Reviewer: &project.RoleSettingsPatch{Agent: sp("claude-code")}}); err != nil {
		t.Fatal(err)
	}
	row, _, _ = store.GetProject(ctx, "cx2")
	if got := row.Config.Reviewers; len(got) != 1 || got[0].Harness != domain.ReviewerClaudeCode || got[0].AgentConfig.Model != "" {
		t.Fatalf("reviewers = %+v, want claude-code with no inherited model", got)
	}

	// No worker agent to follow: the phone must name one.
	seed("none", "", domain.ProjectConfig{})
	if _, err := m.SetRoleSettings(ctx, "none", project.SetRoleSettingsInput{Reviewer: &project.RoleSettingsPatch{Effort: sp("low")}}); err == nil {
		t.Fatal("created a reviewer with no agent and nothing to follow")
	}
}

// A reviewer patch on a project with no reviewer entry seeds it from the app
// default reviewer, so creating the entry never changes who reviews.
func TestSetRoleSettingsSeedsReviewerFromAppDefault(t *testing.T) {
	ctx := context.Background()
	for name, worker := range map[string]domain.AgentHarness{"claude worker": domain.HarnessClaudeCode, "no pinned worker": ""} {
		t.Run(name, func(t *testing.T) {
			store, err := sqlitetest.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			if err := store.SetWorkflowDefaults(ctx, domain.WorkflowDefaults{ReviewerHarness: domain.ReviewerCodex, ReviewerModel: "app-model"}, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			m := project.NewWithDeps(project.Deps{Store: store, Logger: slog.New(slog.DiscardHandler)})
			cfg := domain.ProjectConfig{Worker: domain.RoleOverride{Harness: worker}}
			if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "app", Path: filepath.Join(t.TempDir(), "app"), DisplayName: "App", RegisteredAt: time.Now().UTC(), Config: cfg}); err != nil {
				t.Fatal(err)
			}
			if _, err := m.SetRoleSettings(ctx, "app", project.SetRoleSettingsInput{Reviewer: &project.RoleSettingsPatch{Effort: sp("high")}}); err != nil {
				t.Fatalf("SetRoleSettings: %v", err)
			}
			row, _, _ := store.GetProject(ctx, "app")
			want := []domain.ReviewerConfig{{Harness: domain.ReviewerCodex, AgentConfig: domain.AgentConfig{Model: "app-model", Effort: "high"}}}
			if !reflect.DeepEqual(row.Config.Reviewers, want) {
				t.Fatalf("reviewers = %+v, want %+v", row.Config.Reviewers, want)
			}
		})
	}
}

type failingDefaultsStore struct{ *sqlite.Store }

func (failingDefaultsStore) WorkflowDefaults(context.Context) (domain.WorkflowDefaults, error) {
	return domain.WorkflowDefaults{}, errors.New("read failed")
}

// If the app default cannot be read, a reviewer patch fails rather than pinning
// the worker-derived reviewer for good.
func TestSetRoleSettingsFailsWhenAppDefaultUnreadable(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	cfg := domain.ProjectConfig{Worker: domain.RoleOverride{Harness: domain.HarnessClaudeCode}}
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "app", Path: filepath.Join(t.TempDir(), "app"), DisplayName: "App", RegisteredAt: time.Now().UTC(), Config: cfg}); err != nil {
		t.Fatal(err)
	}
	m := project.NewWithDeps(project.Deps{Store: failingDefaultsStore{store}, Logger: slog.New(slog.DiscardHandler)})
	if _, err := m.SetRoleSettings(ctx, "app", project.SetRoleSettingsInput{Reviewer: &project.RoleSettingsPatch{Effort: sp("high")}}); err == nil {
		t.Fatal("want an error when the app default cannot be read")
	}
	row, _, _ := store.GetProject(ctx, "app")
	if len(row.Config.Reviewers) != 0 {
		t.Fatalf("reviewers = %+v, want none stored", row.Config.Reviewers)
	}
}
