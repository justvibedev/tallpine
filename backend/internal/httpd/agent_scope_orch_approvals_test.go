package httpd

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
	"tallpine.eu/tallpine/backend/internal/sessiontoken"
)

const (
	resolveApp1 = "/api/v1/sessions/app-1/conversation/approvals/req-1/resolve"
	listApp1    = "/api/v1/sessions/app-1/conversation/approvals"
)

// approvalsHarness is the scope gate with the orchestratorApprovals policy
// answered by on(project).
func approvalsHarness(t *testing.T, on func(domain.ProjectID) bool) (agentScopeHarness, *fakeAgentScopeStore, *sessiontoken.Authority) {
	t.Helper()
	store, tokens := newAgentScopeFixture()
	// A second orchestrator in project app, and a worker in project web already exists.
	store.sessions["app-orch2"] = scopeSession("app-orch2", "app", domain.KindOrchestrator, "v-app-orch2")
	logs := &bytes.Buffer{}
	log := slog.New(slog.NewJSONHandler(logs, nil))
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agent, _ := requestscope.AgentFrom(r.Context())
		_ = json.NewEncoder(w).Encode(agent)
	})
	scopes := newAgentScopes(tokens, store, log)
	if on != nil {
		scopes.approvals = func(_ context.Context, p domain.ProjectID) bool { return on(p) }
	}
	h := daemonAuthMiddleware(testDaemonToken, "", nil, scopes)(inner)
	return agentScopeHarness{t: t, handler: h, logs: logs}, store, tokens
}

func TestOrchestratorApprovalsRefusedWhenSettingOff(t *testing.T) {
	for name, on := range map[string]func(domain.ProjectID) bool{
		"no policy engine": nil,
		"policy off":       func(domain.ProjectID) bool { return false },
	} {
		t.Run(name, func(t *testing.T) {
			h, store, tokens := approvalsHarness(t, on)
			h.check(store.tokenFor(tokens, "app-orch"), []scopeCase{
				{"POST", resolveApp1, 403},
				{"GET", listApp1, 403},
			})
		})
	}
}

func TestOrchestratorApprovalsAllowedOnlyForOwnProjectWorkers(t *testing.T) {
	h, store, tokens := approvalsHarness(t, func(p domain.ProjectID) bool { return p == "app" })
	h.check(store.tokenFor(tokens, "app-orch"), []scopeCase{
		// Own project's worker: allowed.
		{"POST", resolveApp1, 200},
		{"GET", listApp1, 200},
		{"POST", "/api/v1/sessions/app-2/conversation/approvals/req-1/resolve", 200},
		// Another project's worker.
		{"POST", "/api/v1/sessions/web-1/conversation/approvals/req-1/resolve", 403},
		{"GET", "/api/v1/sessions/web-1/conversation/approvals", 403},
		// Its own session, and another orchestrator of the project.
		{"POST", "/api/v1/sessions/app-orch/conversation/approvals/req-1/resolve", 403},
		{"GET", "/api/v1/sessions/app-orch/conversation/approvals", 403},
		{"POST", "/api/v1/sessions/app-orch2/conversation/approvals/req-1/resolve", 403},
		// A session that does not exist.
		{"POST", "/api/v1/sessions/nope/conversation/approvals/req-1/resolve", 403},
		// A worker's in-chat question is answered like its approvals, same scope.
		{"POST", "/api/v1/sessions/app-1/conversation/inputs/req-1/resolve", 200},
		{"POST", "/api/v1/sessions/web-1/conversation/inputs/req-1/resolve", 403},
		{"POST", "/api/v1/sessions/app-orch/conversation/inputs/req-1/resolve", 403},
		// Side chats and reviewer conversations stay human-only.
		{"POST", "/api/v1/sessions/app-1/conversation/side-chats/s1/approvals/req-1/resolve", 403},
		{"POST", "/api/v1/reviews/rev-app-1/conversation/approvals/req-1/resolve", 403},
	})
	// The project that did not opt in stays refused even though app did.
	h.check(store.tokenFor(tokens, "web-orch"), []scopeCase{
		{"POST", "/api/v1/sessions/web-1/conversation/approvals/req-1/resolve", 403},
		{"POST", "/api/v1/sessions/web-1/conversation/inputs/req-1/resolve", 403},
	})
}

func TestWorkerAndReviewerTokensNeverResolveApprovals(t *testing.T) {
	h, store, tokens := approvalsHarness(t, func(domain.ProjectID) bool { return true })
	for _, token := range []string{
		store.tokenFor(tokens, "app-1"), // a worker, even for its own prompt
		store.tokenFor(tokens, "app-2"), // a worker for a peer
		tokens.IssueReviewer("rev-app-1", "app-1"),
	} {
		h.check(token, []scopeCase{
			{"POST", resolveApp1, 403},
			{"POST", "/api/v1/sessions/app-2/conversation/approvals/req-1/resolve", 403},
			{"GET", listApp1, 403},
			{"POST", "/api/v1/sessions/app-2/conversation/inputs/req-1/resolve", 403},
		})
	}
}
