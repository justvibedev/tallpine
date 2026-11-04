package controllers_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

func newChangesServer(t *testing.T, store controllers.SettingsChangesStore) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{SettingsChanges: store}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

// B202: GET /settings/changes lists the change log of a project, or of app
// settings when no project is given, newest first and bounded by limit.
func TestSettingsChangesAPI_ListsByProjectNewestFirst(t *testing.T) {
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	at := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	if err := store.AppendSettingsChanges(context.Background(), []domain.SettingsChange{
		{ChangedAt: at, Actor: "user", Key: "app.versionRefresh", OldValue: "true", NewValue: "false"},
		{ChangedAt: at, Actor: "orchestrator:app-1", ProjectID: "app", Key: "project.agentRules", OldValue: "null", NewValue: `"a"`},
		{ChangedAt: at.Add(time.Minute), Actor: "user", ProjectID: "app", Key: "project.noCI", OldValue: "null", NewValue: "true"},
	}); err != nil {
		t.Fatal(err)
	}
	srv := newChangesServer(t, store)
	get := func(query string) controllers.SettingsChangesResponse {
		t.Helper()
		body, status, _ := doRequest(t, srv, "GET", "/api/v1/settings/changes"+query, "")
		if status != http.StatusOK {
			t.Fatalf("GET %s = %d; %s", query, status, body)
		}
		var out controllers.SettingsChangesResponse
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	project := get("?project=app")
	if len(project.Changes) != 2 || project.Changes[0].Key != "project.noCI" ||
		project.Changes[1].Actor != "orchestrator:app-1" || project.Changes[1].NewValue != `"a"` || project.Changes[1].ProjectID != "app" {
		t.Fatalf("project changes = %+v", project.Changes)
	}
	app := get("")
	if len(app.Changes) != 1 || app.Changes[0].Key != "app.versionRefresh" || !app.Changes[0].ChangedAt.Equal(at) {
		t.Fatalf("app changes = %+v", app.Changes)
	}
	if got := get("?project=app&limit=1"); len(got.Changes) != 1 {
		t.Fatalf("limit=1 returned %d", len(got.Changes))
	}
	if got := get("?project=none"); got.Changes == nil || len(got.Changes) != 0 {
		t.Fatalf("unknown project = %+v, want an empty list", got.Changes)
	}
	for _, bad := range []string{"0", "501", "x", "-1"} {
		body, status, _ := doRequest(t, srv, "GET", "/api/v1/settings/changes?limit="+bad, "")
		assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_LIMIT")
	}
}

func TestSettingsChangesAPI_NotImplementedWithoutStore(t *testing.T) {
	body, status, _ := doRequest(t, newChangesServer(t, nil), "GET", "/api/v1/settings/changes", "")
	assertErrorCode(t, body, status, http.StatusNotImplemented, "NOT_IMPLEMENTED")
}
