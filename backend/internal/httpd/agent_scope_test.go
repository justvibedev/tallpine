package httpd

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/daemontoken"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
	"tallpine.eu/tallpine/backend/internal/sessiontoken"
)

type fakeAgentScopeStore struct {
	sessions map[domain.SessionID]domain.SessionRecord
	reviews  map[string]domain.Review
}

func (s *fakeAgentScopeStore) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	rec, ok := s.sessions[id]
	return rec, ok, nil
}

func (s *fakeAgentScopeStore) GetReviewByID(_ context.Context, id string) (domain.Review, bool, error) {
	review, ok := s.reviews[id]
	return review, ok, nil
}

func scopeSession(id string, project string, kind domain.SessionKind, verifier string) domain.SessionRecord {
	rec := domain.SessionRecord{ID: domain.SessionID(id), ProjectID: domain.ProjectID(project), Kind: kind}
	rec.Metadata.BrowserCapabilityVerifier = verifier
	return rec
}

// newAgentScopeFixture is two projects, "app" and "web", each with an
// orchestrator and workers, plus a review of app-1.
func newAgentScopeFixture() (*fakeAgentScopeStore, *sessiontoken.Authority) {
	store := &fakeAgentScopeStore{
		sessions: map[domain.SessionID]domain.SessionRecord{},
		reviews: map[string]domain.Review{
			"rev-app-1": {ID: "rev-app-1", SessionID: "app-1", ProjectID: "app"},
			"rev-app-2": {ID: "rev-app-2", SessionID: "app-2", ProjectID: "app"},
		},
	}
	for _, rec := range []domain.SessionRecord{
		scopeSession("app-1", "app", domain.KindWorker, "v-app-1"),
		scopeSession("app-2", "app", domain.KindWorker, "v-app-2"),
		scopeSession("app-orch", "app", domain.KindOrchestrator, "v-app-orch"),
		scopeSession("web-1", "web", domain.KindWorker, "v-web-1"),
		scopeSession("web-orch", "web", domain.KindOrchestrator, "v-web-orch"),
	} {
		store.sessions[rec.ID] = rec
	}
	dead := scopeSession("app-dead", "app", domain.KindWorker, "v-app-dead")
	dead.IsTerminated = true
	store.sessions[dead.ID] = dead
	return store, sessiontoken.New(testDaemonToken)
}

func (s *fakeAgentScopeStore) tokenFor(tokens *sessiontoken.Authority, id string) string {
	rec := s.sessions[domain.SessionID(id)]
	return tokens.IssueSession(rec.ID, rec.Metadata.BrowserCapabilityVerifier)
}

type agentScopeHarness struct {
	t       *testing.T
	handler http.Handler
	logs    *bytes.Buffer
}

func newAgentScopeHarness(t *testing.T, store AgentScopeStore, tokens *sessiontoken.Authority) agentScopeHarness {
	t.Helper()
	logs := &bytes.Buffer{}
	log := slog.New(slog.NewJSONHandler(logs, nil))
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agent, _ := requestscope.AgentFrom(r.Context())
		_ = json.NewEncoder(w).Encode(agent)
	})
	h := daemonAuthMiddleware(testDaemonToken, "", nil, newAgentScopes(tokens, store, log), nil)(inner)
	return agentScopeHarness{t: t, handler: h, logs: logs}
}

func (h agentScopeHarness) do(token, method, target string) *httptest.ResponseRecorder {
	h.t.Helper()
	req := httptest.NewRequest(method, target, http.NoBody)
	if token != "" {
		req.Header.Set(daemontoken.Header, token)
	}
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	return rec
}

type scopeCase struct {
	method string
	path   string
	want   int
}

func (h agentScopeHarness) check(token string, cases []scopeCase) {
	h.t.Helper()
	for _, tc := range cases {
		rec := h.do(token, tc.method, tc.path)
		if rec.Code != tc.want {
			h.t.Errorf("%s %s = %d, want %d (body %s)", tc.method, tc.path, rec.Code, tc.want, strings.TrimSpace(rec.Body.String()))
		}
	}
}

func TestAgentScopeWorkerActsOnlyOnItself(t *testing.T) {
	store, tokens := newAgentScopeFixture()
	h := newAgentScopeHarness(t, store, tokens)
	h.check(store.tokenFor(tokens, "app-1"), []scopeCase{
		// Its own state, hooks, reports, preview, and renders.
		{"POST", "/api/v1/sessions/app-1/activity", 200},
		{"POST", "/api/v1/sessions/app-1/github-credential", 200},
		{"POST", "/api/v1/sessions/app-1/commit", 200},
		{"POST", "/api/v1/reports", 200},
		{"GET", "/api/v1/sessions/app-1", 200},
		{"HEAD", "/api/v1/sessions/app-1", 200},
		{"GET", "/api/v1/sessions/app-1/workspace/file?path=a.go", 200},
		{"GET", "/api/v1/sessions/app-1/reviews", 200},
		{"POST", "/api/v1/sessions/app-1/reviews/trigger", 200},
		{"POST", "/api/v1/sessions/app-1/pr/claim", 200},
		{"POST", "/api/v1/sessions/app-1/preview", 200},
		{"POST", "/api/v1/sessions/app-1/renders", 200},
		{"GET", "/api/v1/sessions/app-1/agent-browser/json", 200},
		{"GET", "/api/v1/sessions", 200},
		{"GET", "/api/v1/projects/app", 200},
		// Asking its own project's orchestrator.
		{"GET", "/api/v1/sessions/app-orch", 200},
		{"POST", "/api/v1/sessions/app-orch/send", 200},
		{"POST", "/api/v1/sessions/app-orch/conversation/steer-or-send", 200},

		// Never another session, even in its own project.
		{"GET", "/api/v1/sessions/app-2", 403},
		{"GET", "/api/v1/sessions/app-2/workspace/file?path=a.go", 403},
		{"GET", "/api/v1/sessions/app-2/conversation", 403},
		{"GET", "/api/v1/sessions/app-orch/workspace/file?path=a.go", 403},
		{"POST", "/api/v1/sessions/app-2/send", 403},
		{"POST", "/api/v1/sessions/app-2/activity", 403},
		{"POST", "/api/v1/sessions/app-2/github-credential", 403},
		{"POST", "/api/v1/sessions/app-2/commit", 403},
		{"POST", "/api/v1/sessions/app-2/reviews/trigger", 403},
		{"POST", "/api/v1/sessions/web-orch/send", 403},
		// Workers read settings (B202) but never change them.
		{"GET", "/api/v1/settings/values?project=app", 200},
		{"PATCH", "/api/v1/settings/values", 403},
		{"PATCH", "/api/v1/settings/workflow-defaults", 403},
		{"GET", "/api/v1/projects/web", 403},
		// No orchestration, not even of itself.
		{"POST", "/api/v1/sessions", 403},
		{"POST", "/api/v1/sessions/app-1/kill", 403},
		{"POST", "/api/v1/sessions/app-2/kill", 403},
		{"POST", "/api/v1/sessions/app-1/restore", 403},
		{"POST", "/api/v1/sessions/cleanup", 403},
		{"POST", "/api/v1/orchestrators", 403},
		{"POST", "/api/v1/orchestrators/delegate", 403},
		// Handoff replaces an orchestrator: never a worker's call, own or other.
		{"POST", "/api/v1/orchestrators/app-1/handoff", 403},
		{"POST", "/api/v1/orchestrators/app-orch/handoff", 403},
		// Research mode is an orchestrator's, never a worker's.
		{"POST", "/api/v1/orchestrators/app-1/research", 403},
		{"POST", "/api/v1/orchestrators/app-orch/research", 403},
		{"GET", "/api/v1/orchestrators/app-orch/research", 403},
		// Never its own approvals or input requests.
		{"POST", "/api/v1/sessions/app-1/conversation/approvals/req-1/resolve", 403},
		{"POST", "/api/v1/sessions/app-1/conversation/inputs/req-1/resolve", 403},
		{"POST", "/api/v1/sessions/app-1/conversation/side-chats/s1/approvals/req-1/resolve", 403},
		{"PATCH", "/api/v1/sessions/app-1/conversation/settings", 403},
	})
}

func TestAgentScopeOrchestratorIsBoundToItsProject(t *testing.T) {
	store, tokens := newAgentScopeFixture()
	h := newAgentScopeHarness(t, store, tokens)
	h.check(store.tokenFor(tokens, "app-orch"), []scopeCase{
		{"POST", "/api/v1/sessions", 200},
		{"POST", "/api/v1/sessions/cleanup", 200},
		{"POST", "/api/v1/agents/readiness/ensure", 200},
		{"GET", "/api/v1/sessions/app-1", 200},
		{"GET", "/api/v1/sessions/app-1/workspace/file?path=a.go", 200},
		{"GET", "/api/v1/sessions/app-1/conversation", 200},
		{"POST", "/api/v1/sessions/app-1/send", 200},
		{"POST", "/api/v1/sessions/app-1/kill", 200},
		{"POST", "/api/v1/sessions/app-1/restore", 200},
		{"POST", "/api/v1/sessions/app-1/resume-agent", 200},
		{"PATCH", "/api/v1/sessions/app-1", 200},
		{"POST", "/api/v1/sessions/app-1/pr/claim", 200},
		{"POST", "/api/v1/sessions/app-1/reviews/trigger", 200},
		{"GET", "/api/v1/reports?projectId=app", 200},
		// Handoff of its own session only: not a worker, not another
		// project's orchestrator.
		{"POST", "/api/v1/orchestrators/app-orch/handoff", 200},
		{"POST", "/api/v1/orchestrators/app-1/handoff", 403},
		{"POST", "/api/v1/orchestrators/web-orch/handoff", 403},
		// Research mode of its own session only.
		{"POST", "/api/v1/orchestrators/app-orch/research", 200},
		{"GET", "/api/v1/orchestrators/app-orch/research", 200},
		{"POST", "/api/v1/orchestrators/web-orch/research", 403},
		{"POST", "/api/v1/orchestrators/app-1/research", 403},

		// Nothing in another project.
		{"GET", "/api/v1/sessions/web-1", 403},
		{"GET", "/api/v1/sessions/web-1/workspace/file?path=a.go", 403},
		{"POST", "/api/v1/sessions/web-1/send", 403},
		{"POST", "/api/v1/sessions/web-1/kill", 403},
		{"POST", "/api/v1/sessions/web-orch/kill", 403},
		{"GET", "/api/v1/projects/web", 403},
		{"POST", "/api/v1/sessions/missing-1/kill", 403},
		// A worker's own browser and preview stay the worker's.
		{"GET", "/api/v1/sessions/app-1/agent-browser/json", 403},
		{"POST", "/api/v1/sessions/app-1/preview", 403},
		{"POST", "/api/v1/sessions/app-1/renders", 403},
		// No approvals, global settings, project config, or host control.
		{"POST", "/api/v1/sessions/app-1/conversation/approvals/req-1/resolve", 403},
		{"POST", "/api/v1/reviews/rev-app-1/conversation/approvals/req-1/resolve", 403},
		{"PUT", "/api/v1/projects/app/config", 403},
		{"PATCH", "/api/v1/projects/app/permissions", 403},
		{"DELETE", "/api/v1/projects/app", 403},
		{"PATCH", "/api/v1/settings/session-interface", 403},
		{"PATCH", "/api/v1/settings/workflow-defaults", 403},
		{"GET", "/api/v1/settings", 403},
		// Settings by key pass the gate; the handler and settingswrite decide
		// per project and key (B202).
		{"GET", "/api/v1/settings/changes?project=app", 200},
		{"GET", "/api/v1/settings/schema", 200},
		{"GET", "/api/v1/settings/values?project=app", 200},
		{"PATCH", "/api/v1/settings/values", 200},
		{"POST", "/api/v1/shell-terminals", 403},
		{"POST", "/api/v1/mobile/enable", 403},
		{"GET", "/api/v1/link-preview?url=http://169.254.169.254/", 403},
		{"PUT", "/api/v1/github/pat", 403},
		{"POST", "/api/v1/agents/claude-code/install", 403},
		{"POST", "/api/v1/orchestrators", 403},
		{"GET", "/api/v1/events", 403},
		{"GET", "/mux", 403},
		{"POST", "/shutdown", 403},
		{"GET", "/debug/pprof/", 403},
	})
}

func TestAgentScopeReviewerSubmitsOnlyItsReview(t *testing.T) {
	store, tokens := newAgentScopeFixture()
	h := newAgentScopeHarness(t, store, tokens)
	h.check(tokens.IssueReviewer("rev-app-1", "app-1"), []scopeCase{
		{"POST", "/api/v1/reviews/rev-app-1/activity", 200},
		{"POST", "/api/v1/sessions/app-1/reviews/submit", 200},
		// A reviewer posts comments with gh, as the worker under review.
		{"POST", "/api/v1/sessions/app-1/github-credential", 200},
		{"POST", "/api/v1/sessions/app-2/github-credential", 403},
		// A reviewer never commits in the worktree under review.
		{"POST", "/api/v1/sessions/app-1/commit", 403},
		{"GET", "/api/v1/sessions/app-1", 200},
		{"GET", "/api/v1/sessions/app-1/reviews", 200},

		{"POST", "/api/v1/reviews/rev-app-2/activity", 403},
		{"POST", "/api/v1/sessions/app-2/reviews/submit", 403},
		{"GET", "/api/v1/sessions/app-2", 403},
		{"POST", "/api/v1/sessions/app-1/send", 403},
		{"POST", "/api/v1/sessions/app-1/activity", 403},
		{"POST", "/api/v1/reports", 403},
		{"POST", "/api/v1/sessions/app-1/reviews/trigger", 403},
		{"POST", "/api/v1/sessions", 403},
	})
}

func TestAgentScopeRejectsStaleOrForgedTokens(t *testing.T) {
	store, tokens := newAgentScopeFixture()
	h := newAgentScopeHarness(t, store, tokens)
	valid := store.tokenFor(tokens, "app-1")
	if rec := h.do(valid, "GET", "/api/v1/sessions/app-1"); rec.Code != 200 {
		t.Fatalf("valid token = %d", rec.Code)
	}

	for name, token := range map[string]string{
		"terminated session":     store.tokenFor(tokens, "app-dead"),
		"another install":        sessiontoken.New("another-install-token").IssueSession("app-1", "v-app-1"),
		"forged mac":             valid[:len(valid)-4] + "AAAA",
		"unknown session":        tokens.IssueSession("ghost-1", "v"),
		"previous launch":        tokens.IssueSession("app-1", "v-app-1-previous"),
		"reviewer of other work": tokens.IssueReviewer("rev-app-1", "app-2"),
		"unknown review":         tokens.IssueReviewer("rev-ghost", "app-1"),
	} {
		rec := h.do(token, "GET", "/api/v1/sessions/app-1")
		if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "SESSION_TOKEN_INVALID") {
			t.Errorf("%s: got %d %s, want 401 SESSION_TOKEN_INVALID", name, rec.Code, rec.Body.String())
		}
	}

	// Relaunching the session rotates its verifier, which revokes the token
	// the previous launch held.
	relaunched := store.sessions["app-1"]
	relaunched.Metadata.BrowserCapabilityVerifier = "v-app-1-next"
	store.sessions["app-1"] = relaunched
	if rec := h.do(valid, "GET", "/api/v1/sessions/app-1"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("token of a replaced launch = %d, want 401", rec.Code)
	}
	if rec := h.do(store.tokenFor(tokens, "app-1"), "GET", "/api/v1/sessions/app-1"); rec.Code != 200 {
		t.Fatalf("token of the current launch = %d, want 200", rec.Code)
	}

	// Terminating a reviewed worker revokes its reviewer.
	worker := store.sessions["app-2"]
	worker.IsTerminated = true
	store.sessions["app-2"] = worker
	if rec := h.do(tokens.IssueReviewer("rev-app-2", "app-2"), "POST", "/api/v1/reviews/rev-app-2/activity"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("reviewer of a terminated worker = %d, want 401", rec.Code)
	}
}

func TestAgentScopeKeepsInstallTokenUnscoped(t *testing.T) {
	store, tokens := newAgentScopeFixture()
	h := newAgentScopeHarness(t, store, tokens)
	h.check(testDaemonToken, []scopeCase{
		{"POST", "/api/v1/sessions/web-1/conversation/approvals/req-1/resolve", 200},
		{"PATCH", "/api/v1/settings/session-interface", 200},
		{"POST", "/api/v1/sessions/app-2/kill", 200},
	})
	if rec := h.do(testDaemonToken, "GET", "/api/v1/sessions"); strings.Contains(rec.Body.String(), "worker") {
		t.Fatalf("install token request carried an agent principal: %s", rec.Body.String())
	}
	// No token at all is still the install-token 401.
	if rec := h.do("", "GET", "/api/v1/sessions"); rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "DAEMON_TOKEN_REQUIRED") {
		t.Fatalf("missing token = %d %s", rec.Code, rec.Body.String())
	}
}

func TestAgentScopeHandsThePrincipalToHandlers(t *testing.T) {
	store, tokens := newAgentScopeFixture()
	h := newAgentScopeHarness(t, store, tokens)
	rec := h.do(store.tokenFor(tokens, "app-orch"), "GET", "/api/v1/sessions")
	var agent requestscope.Agent
	if err := json.Unmarshal(rec.Body.Bytes(), &agent); err != nil {
		t.Fatal(err)
	}
	want := requestscope.Agent{Role: requestscope.RoleOrchestrator, SessionID: "app-orch", ProjectID: "app"}
	if agent != want {
		t.Fatalf("principal = %+v, want %+v", agent, want)
	}
}

func TestAgentScopeMatchesEscapedPathsAsChiDoes(t *testing.T) {
	store, tokens := newAgentScopeFixture()
	h := newAgentScopeHarness(t, store, tokens)
	h.check(store.tokenFor(tokens, "app-1"), []scopeCase{
		// An encoded slash keeps "app-1/../app-2" inside the {sessionId}
		// segment, exactly as chi routes it; it is not the caller's id.
		{"GET", "/api/v1/sessions/app-1%2F..%2Fapp-2", 403},
		{"POST", "/api/v1/sessions/app%2D1/activity", 200},
		{"GET", "/api/v1/sessions//workspace/file", 403},
		{"GET", "/api/v1/sessions/app-1/extra/segments/kill", 403},
	})
}

func TestAgentScopeAuditsEveryRefusal(t *testing.T) {
	store, tokens := newAgentScopeFixture()
	h := newAgentScopeHarness(t, store, tokens)
	h.do(store.tokenFor(tokens, "app-1"), "POST", "/api/v1/sessions/app-1/conversation/approvals/req-9/resolve")
	h.do(tokens.IssueSession("app-1", "stale"), "GET", "/api/v1/sessions/app-1")

	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(h.logs.String()), "\n") {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		records = append(records, record)
	}
	if len(records) != 2 {
		t.Fatalf("audit records = %d, want 2: %s", len(records), h.logs.String())
	}
	forbidden, unauthenticated := records[0], records[1]
	for key, want := range map[string]string{
		"audit": "agent_scope", "outcome": "forbidden", "role": "worker", "session": "app-1", "project": "app",
		"method": "POST", "path": "/api/v1/sessions/app-1/conversation/approvals/req-9/resolve",
	} {
		if forbidden[key] != want {
			t.Errorf("forbidden record %s = %v, want %q", key, forbidden[key], want)
		}
	}
	if unauthenticated["outcome"] != "unauthenticated" || unauthenticated["claimed_id"] != "app-1" {
		t.Errorf("unauthenticated record = %v", unauthenticated)
	}
	if strings.Contains(h.logs.String(), tokens.IssueSession("app-1", "stale")) {
		t.Fatal("audit log recorded a token")
	}
}

// Every allowlisted route must exist: a typo would silently deny agents, and
// a rule for a route that no longer exists is dead policy.
func TestAgentRulesNameRealRoutes(t *testing.T) {
	spec := apispec.Default()
	for _, rule := range agentRules {
		for _, method := range strings.Split(rule.methods, ",") {
			if spec.Operation(method, rule.pattern) == nil {
				t.Errorf("agent rule %s %s is not an API operation", method, rule.pattern)
			}
		}
		if len(rule.roles) == 0 {
			t.Errorf("agent rule %s %s grants no role", rule.methods, rule.pattern)
		}
	}
}

func TestAgentScopeGuardsTheWholeRouter(t *testing.T) {
	store, tokens := newAgentScopeFixture()
	srv := httptest.NewServer(NewRouterWithControl(config.Config{AllowedOrigins: []string{"app://renderer"}}, discardLogger(), nil,
		APIDeps{AgentScopes: store}, ControlDeps{DaemonToken: testDaemonToken, RequestShutdown: func() {}}))
	t.Cleanup(srv.Close)
	worker := store.tokenFor(tokens, "app-1")
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{"POST", "/shutdown", 403},
		{"GET", "/mux", 403},
		{"GET", "/healthz", 200},
		// Routed to its (unwired) handler: authorized, then 501.
		{"GET", "/api/v1/sessions/app-1", 501},
	} {
		resp := doAuthRequest(t, tc.method, srv.URL+tc.path, http.Header{daemontoken.Header: {worker}})
		if resp.StatusCode != tc.want {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.path, resp.StatusCode, tc.want)
		}
	}
}

// "Update & restart" replaces the app and daemon: only the owner's install
// token may use it. No agent rule names the route, so every agent role is
// refused; the install token passes.
func TestAgentScopeRefusesUpdateRoutesToEveryAgent(t *testing.T) {
	store, tokens := newAgentScopeFixture()
	h := newAgentScopeHarness(t, store, tokens)
	for _, id := range []string{"app-1", "app-orch"} {
		h.check(store.tokenFor(tokens, id), []scopeCase{
			{"GET", "/api/v1/update", 403},
			{"POST", "/api/v1/update", 403},
		})
	}
	h.check(testDaemonToken, []scopeCase{
		{"GET", "/api/v1/update", 200},
		{"POST", "/api/v1/update", 200},
	})
}

func TestAgentScopeSilentFailuresOrchestratorOnly(t *testing.T) {
	store, tokens := newAgentScopeFixture()
	h := newAgentScopeHarness(t, store, tokens)
	h.check(tokens.IssueSession("app-orch", "v-app-orch"), []scopeCase{
		{"GET", "/api/v1/health/silent-failures", 200},
	})
	h.check(tokens.IssueSession("app-1", "v-app-1"), []scopeCase{
		{"GET", "/api/v1/health/silent-failures", 403},
	})
}

// B222: a send to a deleted session must say it was not found, not that it
// "targets another session".
func TestAgentScopeMissingTargetSaysNotFound(t *testing.T) {
	store, tokens := newAgentScopeFixture()
	h := newAgentScopeHarness(t, store, tokens)
	rec := h.do(store.tokenFor(tokens, "app-orch"), "POST", "/api/v1/sessions/gone-1/send")
	body := rec.Body.String()
	if !strings.Contains(body, "was not found") || strings.Contains(body, "targets another session") {
		t.Fatalf("body = %s, want a not-found reason", body)
	}
}
