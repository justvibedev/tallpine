package httpd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/config"
)

// A new route is unreachable from the network until someone classifies it, and
// this test makes the omission loud: every mounted route must match a rule.
func TestEveryMountedRouteIsClassifiedForRemoteAccess(t *testing.T) {
	// Routes mounted only when a dependency is present need that dependency
	// here, or they escape the walk (the live-browser route did).
	router := NewRouterWithControl(config.Config{RequestTimeout: time.Second}, discardLogger(), nil, APIDeps{BrowserLive: NewBrowserLiveHub(nil, nil, nil, nil)}, ControlDeps{})
	seen := 0
	sawBrowserLive := false
	err := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		path := routeParam.ReplaceAllString(strings.ReplaceAll(route, "/*", "/x"), "x")
		seen++
		sawBrowserLive = sawBrowserLive || strings.HasSuffix(route, "/browser/live")
		if classifyLANRoute(method, path) == lanRouteUnclassified {
			t.Errorf("%s %s is not in lanRouteTable; classify it mobile or loopback-only", method, route)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sawBrowserLive {
		t.Fatal("conditionally mounted live-browser route was not walked")
	}
	if seen < 200 {
		t.Fatalf("walked only %d routes; router not mounted?", seen)
	}
}

func TestRemoteListenerRefusesAdminRoutes(t *testing.T) {
	for _, c := range []struct{ method, path string }{
		{http.MethodPut, "/api/v1/projects/p1"},
		{http.MethodPut, "/api/v1/projects/p1/config"},
		{http.MethodPut, "/api/v1/projects/p1/role-settings"},
		{http.MethodGet, "/api/v1/projects/p1/role-settings"},
		{http.MethodPost, "/api/v1/projects/p1/role-settings"},
		{http.MethodPatch, "/api/v1/projects/p1/role-settings/x"},
		{http.MethodPatch, "/api/v1/projects/p1/config"},
		{http.MethodPatch, "/api/v1/projects/p1/agent-policy"},
		{http.MethodGet, "/api/v1/projects/p1/agent-policy"},
		{http.MethodPost, "/api/v1/projects/p1/merges"},
		{http.MethodPost, "/api/v1/projects/p1/backlog"},
		{http.MethodGet, "/api/v1/projects/p1/backlog/B12"},
		{http.MethodPatch, "/api/v1/projects/p1/backlog/B12"},
		{http.MethodDelete, "/api/v1/projects/p1/backlog/B12"},
		{http.MethodPost, "/api/v1/projects/p1/backlog/reorder"},
		{http.MethodGet, "/api/v1/projects/p1/backlog/B12/links"},
		{http.MethodPut, "/api/v1/projects/p1/merges"},
		{http.MethodGet, "/api/v1/projects/p1/merges/extra"},
		{http.MethodPatch, "/api/v1/settings/agent-policy"},
		{http.MethodPut, "/api/v1/github/pat"},
		{http.MethodDelete, "/api/v1/github/pat"},
		{http.MethodPost, "/api/v1/agents/codex/install"},
		{http.MethodPost, "/api/v1/agents/claude/logout"},
		{http.MethodPost, "/api/v1/sessions/s1/github-credential"},
		{http.MethodPatch, "/api/v1/sessions/s1/merge-policy"},
		{http.MethodPut, "/api/v1/sessions/s1/workspace/file"},
		{http.MethodPost, "/api/v1/automations"},
		{http.MethodPost, "/shutdown"},
	} {
		// Over a tailnet-style loopback proxy, so only the route gate can refuse.
		if w := plainRequest(c.method, c.path, "127.0.0.1:5000"); w.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404 on the remote listener", c.method, c.path, w.Code)
		}
	}
}

func TestRemoteListenerKeepsMobileRoutes(t *testing.T) {
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/sessions"},
		{http.MethodPost, "/api/v1/sessions/s1/conversation/messages"},
		{http.MethodPost, "/api/v1/sessions/s1/conversation/approvals/r1/resolve"},
		{http.MethodGet, "/api/v1/sessions/s1/pr"},
		{http.MethodGet, "/api/v1/sessions/s1/preview/files/a/b.png"},
		{http.MethodGet, "/api/v1/sessions/s1/browser/live"},
		{http.MethodGet, "/api/v1/projects"},
		{http.MethodGet, "/api/v1/projects/p1"},
		{http.MethodGet, "/api/v1/projects/p1/merges"},
		{http.MethodGet, "/api/v1/projects/p1/backlog"},
		{http.MethodPatch, "/api/v1/projects/p1/role-settings"},
		{http.MethodGet, "/api/v1/settings"},
		{http.MethodPost, "/api/v1/push/devices"},
		{http.MethodPost, "/pair/exchange"},
		{http.MethodGet, "/api/v1/identity"},
	} {
		if w := plainRequest(c.method, c.path, "127.0.0.1:5000"); w.Code != http.StatusNoContent {
			t.Errorf("%s %s = %d, want pass-through", c.method, c.path, w.Code)
		}
	}
}

func TestRemoteListenerRefusesEncodedSlashPaths(t *testing.T) {
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/sessions/s1%2Fconversation%2Fx/agent-policy", nil)
	r.RemoteAddr = "127.0.0.1:5000"
	h := lanControlBlock(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("encoded-slash path = %d, want 404", w.Code)
	}
}
