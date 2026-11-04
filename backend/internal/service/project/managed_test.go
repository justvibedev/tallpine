package project_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/managedcfg"
	"tallpine.eu/tallpine/backend/internal/service/project"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

// A managed project.models applies to every project read through the real store
// and cannot be changed through SetConfig; other fields still can.
func TestManagedProjectSettingsApplyToEveryProjectAndRefuseChanges(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	locks := managedcfg.New(func() map[string]json.RawMessage {
		return map[string]json.RawMessage{"project.models": json.RawMessage(`{"codexDefault":"gpt-managed"}`)}
	})
	store.SetManaged(locks)
	svc := project.New(store)
	svc.SetManaged(locks)
	for _, id := range []string{"one", "two"} {
		rec := domain.ProjectRecord{ID: id, Path: t.TempDir(), DisplayName: id, RegisteredAt: time.Now(),
			Config: domain.ProjectConfig{Models: domain.ModelPolicy{CodexDefault: "user-choice"}}}
		if err := store.UpsertProject(ctx, rec); err != nil {
			t.Fatal(err)
		}
		got, ok, err := store.GetProject(ctx, id)
		if err != nil || !ok || got.Config.Models.CodexDefault != "gpt-managed" {
			t.Fatalf("%s: models = %+v ok=%v err=%v, want the managed value", id, got.Config.Models, ok, err)
		}
	}
	rec, _, _ := store.GetProject(ctx, "one")

	changed := rec.Config
	changed.Models.CodexDefault = "other"
	_, err = svc.SetConfig(ctx, "one", project.SetConfigInput{Config: changed})
	var api *apierr.Error
	if !errors.As(err, &api) || api.Code != managedcfg.CodeLocked {
		t.Fatalf("SetConfig(models changed) err = %v, want SETTING_LOCKED", err)
	}
	_, err = svc.UpdateSettings(ctx, "one", project.UpdateSettingsInput{DisplayName: "one", Config: changed})
	if !errors.As(err, &api) || api.Code != managedcfg.CodeLocked {
		t.Fatalf("UpdateSettings(models changed) err = %v, want SETTING_LOCKED", err)
	}
	ok := rec.Config
	ok.AutoReview = domain.BoolPtr(true)
	if _, err := svc.SetConfig(ctx, "one", project.SetConfigInput{Config: ok}); err != nil {
		t.Fatalf("SetConfig with an unrelated field: %v", err)
	}
}

// A save made while a profile is installed must not turn the managed value
// into the project's own, and a partial managed object replaces the stored one.
func TestManagedProjectValuesAreNeverPersistedAndReplaceStoredOnes(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	managed := true
	locks := managedcfg.New(func() map[string]json.RawMessage {
		if !managed {
			return nil
		}
		return map[string]json.RawMessage{"project.models": json.RawMessage(`{"denylist":["bad"]}`)}
	})
	store.SetManaged(locks)
	svc := project.New(store)
	svc.SetManaged(locks)
	own := domain.ModelPolicy{CodexDefault: "mine", Allowlist: []string{"keep"}}
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "p", Path: t.TempDir(), DisplayName: "p", RegisteredAt: time.Now(), Config: domain.ProjectConfig{Models: own}}); err != nil {
		t.Fatal(err)
	}
	managed = true
	rec, _, _ := store.GetProject(ctx, "p")
	if rec.Config.Models.CodexDefault != "" || len(rec.Config.Models.Allowlist) != 0 || len(rec.Config.Models.Denylist) != 1 {
		t.Fatalf("effective models = %+v, want exactly the managed {denylist:[bad]}", rec.Config.Models)
	}
	next := rec.Config
	next.AutoReview = domain.BoolPtr(true)
	if _, err := svc.SetConfig(ctx, "p", project.SetConfigInput{Config: next}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateSettings(ctx, "p", project.UpdateSettingsInput{DisplayName: "p2", Config: next}); err != nil {
		t.Fatal(err)
	}
	managed = false
	rec, _, _ = store.GetProject(ctx, "p")
	if rec.Config.AutoReview == nil || !*rec.Config.AutoReview || rec.Config.Models.CodexDefault != "mine" || len(rec.Config.Models.Allowlist) != 1 || len(rec.Config.Models.Denylist) != 0 {
		t.Fatalf("after the profile is removed: %+v, want the user's own models back and the unrelated edit kept", rec.Config)
	}
}
