package httpd

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/daemontoken"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

type fixedCreators map[domain.ProjectID]domain.ProjectID

func (c fixedCreators) SetProjectCreator(context.Context, domain.ProjectID, domain.ProjectID, time.Time) error {
	return nil
}

func (c fixedCreators) ProjectCreator(_ context.Context, p domain.ProjectID) (domain.ProjectID, bool, error) {
	creator, ok := c[p]
	return creator, ok, nil
}

// Every allowed cross-project request is written to the audit log; an
// own-project request is not.
func TestCrossProjectAllowedRequestsAreAudited(t *testing.T) {
	store, tokens := newAgentScopeFixture()
	logs := &bytes.Buffer{}
	scopes := newAgentScopes(tokens, store, slog.New(slog.NewJSONHandler(logs, nil)))
	scopes.crossProject = func(_ context.Context, p domain.ProjectID) bool { return p == "app" }
	scopes.creators = fixedCreators{"lab": "app"}
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := agentScopeHarness{t: t, handler: daemonAuthMiddleware(testDaemonToken, "", nil, scopes)(inner), logs: logs}
	token := store.tokenFor(tokens, "app-orch")

	if rec := h.do(token, "GET", "/api/v1/sessions/app-orch"); rec.Code != 200 {
		t.Fatalf("own session = %d", rec.Code)
	}
	if strings.Contains(logs.String(), "cross_project_allowed") {
		t.Fatalf("an own-project request was audited as cross-project: %s", logs)
	}
	if rec := h.do(token, "GET", "/api/v1/orchestrators/web-orch"); rec.Code != 200 {
		t.Fatalf("cross-project read = %d", rec.Code)
	}
	if rec := h.do(token, "GET", "/api/v1/projects/lab"); rec.Code != 200 {
		t.Fatalf("created project read = %d", rec.Code)
	}
	got := logs.String()
	for _, want := range []string{`"outcome":"cross_project_allowed"`, `"action":"orchestrator_access"`, `"action":"created_project_access"`, `"project":"app"`, `"path":"/api/v1/orchestrators/web-orch"`} {
		if !strings.Contains(got, want) {
			t.Errorf("audit log lacks %s: %s", want, got)
		}
	}
	// A project that is neither the caller's nor created by it is refused.
	if rec := h.do(token, "GET", "/api/v1/projects/web"); rec.Code != http.StatusForbidden {
		t.Errorf("another project = %d, want 403", rec.Code)
	}
}

// Create, remove, a person re-registers the id: the old creator's PUT config is
// refused (real store, so the archive path really clears the creator row).
func TestCrossProjectCreatorLosesRightsWhenProjectIsRemovedAndReregistered(t *testing.T) {
	db, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := db.UpsertProject(ctx, domain.ProjectRecord{ID: "lab", Path: "/p/lab", RegisteredAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetProjectCreator(ctx, "lab", "app", time.Now()); err != nil {
		t.Fatal(err)
	}
	store, tokens := newAgentScopeFixture()
	scopes := newAgentScopes(tokens, store, nil)
	scopes.crossProject = func(context.Context, domain.ProjectID) bool { return true }
	scopes.creators = db
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := agentScopeHarness{t: t, handler: daemonAuthMiddleware(testDaemonToken, "", nil, scopes)(inner), logs: &bytes.Buffer{}}
	token := store.tokenFor(tokens, "app-orch")
	put := func() int {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/projects/lab/config", strings.NewReader(`{"config":{}}`))
		req.Header.Set(daemontoken.Header, token)
		rec := httptest.NewRecorder()
		h.handler.ServeHTTP(rec, req)
		return rec.Code
	}
	if got := put(); got != http.StatusOK {
		t.Fatalf("creator PUT config = %d, want 200", got)
	}
	if ok, err := db.ArchiveProject(ctx, "lab", time.Now()); err != nil || !ok {
		t.Fatalf("archive: %v %v", ok, err)
	}
	if err := db.UpsertProject(ctx, domain.ProjectRecord{ID: "lab", Path: "/p/other", RegisteredAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if got := put(); got != http.StatusForbidden {
		t.Fatalf("PUT config on a re-registered project = %d, want 403", got)
	}
}
