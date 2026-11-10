package httpd

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

type evalWorld struct {
	h      agentScopeHarness
	tokens map[string]string
	cross  *bool
}

// evalFixture: "app" is the caller. lab and plain were created by app, only lab
// is marked eval; web is marked eval but no orchestrator created it.
func evalFixture(t *testing.T) evalWorld {
	t.Helper()
	store, tokens := newAgentScopeFixture()
	for _, rec := range []domain.SessionRecord{
		scopeSession("lab-1", "lab", domain.KindWorker, "v-lab-1"),
		scopeSession("plain-1", "plain", domain.KindWorker, "v-plain-1"),
	} {
		store.sessions[rec.ID] = rec
	}
	cross := true
	scopes := newAgentScopes(tokens, store, nil)
	scopes.crossProject = func(_ context.Context, p domain.ProjectID) bool { return cross && p == "app" }
	scopes.creators = fixedCreators{"lab": "app", "plain": "app"}
	marked := map[domain.ProjectID]bool{"lab": true, "web": true}
	scopes.evalMarked = func(_ context.Context, p domain.ProjectID) bool { return marked[p] }
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := agentScopeHarness{t: t, handler: daemonAuthMiddleware(testDaemonToken, "", nil, scopes, nil)(inner), logs: &bytes.Buffer{}}
	return evalWorld{h: h, cross: &cross, tokens: map[string]string{"orch": store.tokenFor(tokens, "app-orch"), "worker": store.tokenFor(tokens, "app-1")}}
}

// evalRoutes are the routes the owner allowed on a created, eval-marked project.
func evalRoutes(project, session string) []string {
	p := "/api/v1/projects/" + project + "/backlog"
	return []string{
		"GET " + p, "POST " + p, "GET " + p + "/B1", "POST " + p + "/B1/state",
		"GET " + p + "/B1/plan", "POST " + p + "/B1/plan", "POST " + p + "/B1/plan/answers",
		"POST " + p + "/B1/plan/defaults", "POST " + p + "/B1/plan/approve",
		"GET /api/v1/sessions/" + session, "POST /api/v1/sessions/" + session + "/send",
		"GET /api/v1/sessions/" + session + "/usage", "POST /api/v1/sessions/" + session + "/kill",
	}
}

func (w evalWorld) status(token, route string) int {
	method, path, _ := cutSpace(route)
	return w.h.do(token, method, path).Code
}

func cutSpace(s string) (string, string, bool) {
	for i := range s {
		if s[i] == ' ' {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

func TestEvalProjectRoutesOpenOnlyForCreatorOnMarkedProject(t *testing.T) {
	w := evalFixture(t)
	for _, route := range evalRoutes("lab", "lab-1") {
		if got := w.status(w.tokens["orch"], route); got != http.StatusOK {
			t.Errorf("creator on its eval project: %s = %d, want 200", route, got)
		}
	}
	refusals := map[string][]string{
		"created but not marked eval": evalRoutes("plain", "plain-1"),
		"marked eval but not created": evalRoutes("web", "web-1"),
	}
	for why, routes := range refusals {
		for _, route := range routes {
			if got := w.status(w.tokens["orch"], route); got != http.StatusForbidden {
				t.Errorf("%s: %s = %d, want 403", why, route, got)
			}
		}
	}
	for _, route := range evalRoutes("lab", "lab-1") {
		if got := w.status(w.tokens["worker"], route); got != http.StatusForbidden {
			t.Errorf("worker token: %s = %d, want 403", route, got)
		}
	}
	// Cross-project orchestration off for the caller closes it again.
	*w.cross = false
	for _, route := range evalRoutes("lab", "lab-1") {
		if got := w.status(w.tokens["orch"], route); got != http.StatusForbidden {
			t.Errorf("policy off: %s = %d, want 403", route, got)
		}
	}
}

// Everything the owner did not list stays refused on the eval project too.
func TestEvalProjectKeepsEverythingElseRefused(t *testing.T) {
	w := evalFixture(t)
	for _, route := range []string{
		"PATCH /api/v1/projects/lab/backlog/B1", "DELETE /api/v1/projects/lab/backlog/B1", "POST /api/v1/projects/lab/backlog/purge",
		"POST /api/v1/projects/lab/backlog/reorder", "POST /api/v1/projects/lab/backlog/B1/plan/replan", "POST /api/v1/projects/lab/backlog/B1/plan/build-anyway",
		"POST /api/v1/projects/lab/backlog/B1/links", "GET /api/v1/projects/lab/backlog-runs", "POST /api/v1/projects/lab/cues",
		"POST /api/v1/sessions/lab-1/restore", "PATCH /api/v1/sessions/lab-1", "GET /api/v1/sessions/lab-1/conversation",
		"GET /api/v1/sessions/lab-1/workspace/files", "POST /api/v1/sessions/lab-1/reviews/trigger", "GET /api/v1/sessions/lab-1/history",
		"POST /api/v1/sessions/lab-1/conversation/steer-or-send", "POST /api/v1/sessions/lab-1/pr/claim",
	} {
		if got := w.status(w.tokens["orch"], route); got != http.StatusForbidden {
			t.Errorf("%s = %d, want 403", route, got)
		}
	}
}
