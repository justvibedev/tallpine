package httpd

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/daemontoken"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/service/githubcred"
)

type chainAccounts struct{ token string }

func (a chainAccounts) TokenForProject(context.Context, string, string) (string, error) {
	return a.token, nil
}

type chainProjects struct{}

func (chainProjects) GetProject(_ context.Context, id string) (domain.ProjectRecord, bool, error) {
	return domain.ProjectRecord{ID: id, RepoOriginURL: "git@github.com:acme/app.git"}, true, nil
}

// The real chain a helper-mode agent's request takes: the scoped session token
// is verified, the route is authorised for the caller's own session, the policy
// is decided now and the service issues (or refuses) the credential.
func TestGitHubCredentialRouteEndToEndWithScopedTokens(t *testing.T) {
	store, tokens := newAgentScopeFixture()
	serve := func(mode string) http.Handler {
		engine, _ := policytest.Engine(t, map[string]map[string]any{"githubCreds": {"mode": mode}})
		svc := githubcred.New(engine, store, chainProjects{}, chainAccounts{token: "account-secret"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		router := chi.NewRouter()
		router.Route("/api/v1", func(r chi.Router) { (&controllers.GitHubCredentialController{Svc: svc}).Register(r) })
		return daemonAuthMiddleware(testDaemonToken, "", nil, newAgentScopes(tokens, store, slog.New(slog.NewTextHandler(io.Discard, nil))), nil)(router)
	}
	post := func(h http.Handler, token, session string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/"+session+"/github-credential", strings.NewReader(`{"host":"github.com"}`))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set(daemontoken.Header, token)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	helper := serve("helper")
	own := store.tokenFor(tokens, "app-1")
	if rec := post(helper, own, "app-1"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "account-secret") || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("own session in helper mode: %d %s %v", rec.Code, rec.Body, rec.Header())
	}
	if rec := post(helper, own, "app-2"); rec.Code != 403 || strings.Contains(rec.Body.String(), "account-secret") {
		t.Fatalf("an agent must not ask for another session's credential: %d %s", rec.Code, rec.Body)
	}
	if rec := post(helper, "", "app-1"); rec.Code == 200 {
		t.Fatalf("no credential without a token: %d", rec.Code)
	}
	if rec := post(helper, tokens.IssueReviewer("rev-app-1", "app-1"), "app-1"); rec.Code != 200 {
		t.Fatalf("a reviewer gets the reviewed worker's credential: %d %s", rec.Code, rec.Body)
	}
	if rec := post(serve("env"), own, "app-1"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "account-secret") {
		t.Fatalf("env mode serves a session launched under helper (Security mode turned off mid-run): %d %s", rec.Code, rec.Body)
	}
	if rec := post(serve("none"), own, "app-1"); rec.Code != 403 || !strings.Contains(rec.Body.String(), "GITHUB_CREDENTIALS_DENIED") || strings.Contains(rec.Body.String(), "account-secret") {
		t.Fatalf("mode none refuses every request: %d %s", rec.Code, rec.Body)
	}
}
