package store_test

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestSettingsChangesAppendAndListByProject(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	changes := []domain.SettingsChange{
		{ChangedAt: at, Actor: "user", Key: "app.versionRefresh", OldValue: "true", NewValue: "false"},
		{ChangedAt: at, Actor: "orchestrator:app-1", ProjectID: "app", Key: "project.agentRules", OldValue: `""`, NewValue: `"x"`},
		{ChangedAt: at.Add(time.Second), Actor: "user", ProjectID: "app", Key: "project.noCI", OldValue: "false", NewValue: "true"},
	}
	if err := s.AppendSettingsChanges(ctx, changes); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListSettingsChanges(ctx, "app", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Key != "project.noCI" || got[1].Actor != "orchestrator:app-1" || got[1].NewValue != `"x"` {
		t.Fatalf("project changes = %+v", got)
	}
	app, _ := s.ListSettingsChanges(ctx, "", 10)
	if len(app) != 1 || app[0].Key != "app.versionRefresh" || !app[0].ChangedAt.Equal(at) {
		t.Fatalf("app changes = %+v", app)
	}
	if one, _ := s.ListSettingsChanges(ctx, "app", 1); len(one) != 1 {
		t.Fatalf("limit ignored: %d rows", len(one))
	}
}
