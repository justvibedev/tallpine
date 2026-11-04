package managedcfg

import (
	"encoding/json"
	"errors"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
)

func TestNilLocksLockNothing(t *testing.T) {
	var l *Locks
	if l.Locked("app.x") || l.Refuse("app.x") != nil {
		t.Fatal("a nil Locks locked something")
	}
	cfg := domain.ProjectConfig{AutoReview: domain.BoolPtr(true)}
	if got := l.ApplyProject(cfg); got.AutoReview == nil || !*got.AutoReview {
		t.Fatal("a nil Locks changed a config")
	}
}

func TestRefuseNamesLockedKeys(t *testing.T) {
	l := New(func() map[string]json.RawMessage { return map[string]json.RawMessage{"app.b": nil, "app.a": nil} })
	err := l.Refuse("app.b", "app.free", "app.a")
	var api *apierr.Error
	if !errors.As(err, &api) || api.Code != CodeLocked || api.Kind != apierr.KindConflict {
		t.Fatalf("err = %v, want a 409 SETTING_LOCKED", err)
	}
	if keys, _ := api.Details["keys"].([]string); len(keys) != 2 || keys[0] != "app.a" || keys[1] != "app.b" {
		t.Errorf("keys = %v, want [app.a app.b]", api.Details["keys"])
	}
}

func TestApplyProjectKeepsUnlockedFieldsAndIgnoresUnusableValues(t *testing.T) {
	l := New(func() map[string]json.RawMessage {
		return map[string]json.RawMessage{
			"project.models":        json.RawMessage(`{"codexDefault":"m"}`),
			"project.trackerIntake": nil,
		}
	})
	in := domain.ProjectConfig{AutoReview: domain.BoolPtr(true), SessionPrefix: "x"}
	got := l.ApplyProject(in)
	if got.Models.CodexDefault != "m" || got.AutoReview == nil || !*got.AutoReview || got.SessionPrefix != "x" {
		t.Fatalf("got %+v", got)
	}
	if keys := l.LockedProjectChanges(in, got); len(keys) != 1 || keys[0] != "project.models" {
		t.Errorf("LockedProjectChanges = %v, want only project.models", keys)
	}
	if keys := l.LockedProjectChanges(got, got); len(keys) != 0 {
		t.Errorf("identical configs reported changes: %v", keys)
	}
}

func TestManagedAutoReviewLockBeatsAppDefault(t *testing.T) {
	l := New(func() map[string]json.RawMessage {
		return map[string]json.RawMessage{"project.autoReview": json.RawMessage(`false`)}
	})
	cfg := l.ApplyProject(domain.ProjectConfig{})
	if cfg.EffectiveAutoReview(domain.WorkflowDefaults{AutoReview: true}) {
		t.Fatal("managed project.autoReview=false lost to the app default")
	}
}
