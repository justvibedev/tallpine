package httpd

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/daemontoken"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/settingsreg"
)

// An orchestrator may turn planInPlace on only for a project it registered
// itself; its own project, another project, and one the owner created stay
// the owner's (B335 eval projects).
func TestPlanInPlaceConfigOnlyOnCreatedProjects(t *testing.T) {
	store, tokens := newAgentScopeFixture()
	scopes := newAgentScopes(tokens, store, nil)
	scopes.crossProject = func(_ context.Context, p domain.ProjectID) bool { return p == "app" }
	scopes.creators = fixedCreators{"lab": "app"} // "web" has no recorded creator: the owner made it
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := agentScopeHarness{t: t, handler: daemonAuthMiddleware(testDaemonToken, "", nil, scopes)(inner), logs: &bytes.Buffer{}}
	token := store.tokenFor(tokens, "app-orch")
	put := func(project string) int {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/projects/"+project+"/config", strings.NewReader(`{"config":{"planInPlace":true}}`))
		req.Header.Set(daemontoken.Header, token)
		rec := httptest.NewRecorder()
		h.handler.ServeHTTP(rec, req)
		return rec.Code
	}
	if got := put("lab"); got != http.StatusOK {
		t.Errorf("own created project = %d, want 200", got)
	}
	// A worker of the creating project is refused on the project routes.
	workerToken := store.tokenFor(tokens, "app-1")
	workerPut := httptest.NewRequest(http.MethodPut, "/api/v1/projects/lab/config", strings.NewReader(`{"config":{"planInPlace":true}}`))
	workerPut.Header.Set(daemontoken.Header, workerToken)
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, workerPut)
	if rec.Code != http.StatusForbidden {
		t.Errorf("worker PUT config = %d, want 403", rec.Code)
	}
	for _, other := range []string{"web", "app"} {
		if got := put(other); got != http.StatusForbidden {
			t.Errorf("%s (owner-created or the orchestrator's own project) = %d, want 403", other, got)
		}
	}
}

// The registry lets the user and the creating orchestrator write planInPlace,
// never a project's own orchestrator or a worker; planAutoApprove stays the
// user's alone.
func TestPlanInPlaceWriters(t *testing.T) {
	reg := settingsreg.Default()
	inPlace, _ := reg.Lookup("project.planInPlace")
	for actor, want := range map[settingsreg.Actor]bool{
		settingsreg.ActorUser: true, settingsreg.ActorCreatorOrchestrator: true, settingsreg.ActorOrchestrator: false, settingsreg.ActorWorker: false,
	} {
		if got := inPlace.CanWrite(actor); got != want {
			t.Errorf("planInPlace CanWrite(%s) = %v, want %v", actor, got, want)
		}
	}
	if auto, _ := reg.Lookup("project.planAutoApprove"); auto.CanWrite(settingsreg.ActorCreatorOrchestrator) || auto.CanWrite(settingsreg.ActorOrchestrator) {
		t.Error("planAutoApprove must stay human-only")
	}
	if why := agentConfigViolation([]byte(`{"planInPlace":true}`)); why != "" {
		t.Errorf("planInPlace violation = %q, want none", why)
	}
}
