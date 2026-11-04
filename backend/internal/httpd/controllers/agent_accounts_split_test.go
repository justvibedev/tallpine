package controllers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/service/agentaccounts"
)

type splitAccountsService struct {
	controllers.AgentAccountService
	views []agentaccounts.AccountView
	err   error
}

func (s splitAccountsService) List(context.Context, bool) ([]agentaccounts.AccountView, error) {
	return s.views, nil
}
func (s splitAccountsService) SetDefault(context.Context, string) error { return s.err }

func serveAccounts(svc controllers.AgentAccountService, method, path string) *httptest.ResponseRecorder {
	router := chi.NewRouter()
	(&controllers.AgentAccountsController{Svc: svc}).Register(router)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestAgentAccountsListCarriesHistorySplit(t *testing.T) {
	rec := serveAccounts(splitAccountsService{views: []agentaccounts.AccountView{
		{Account: domain.AgentAccount{ID: "a", Provider: domain.AgentAccountProviderClaude}, HistorySplit: []string{"projects", "tasks"}},
		{Account: domain.AgentAccount{ID: "b", Provider: domain.AgentAccountProviderClaude}, HistorySplit: []string{}},
	}}, http.MethodGet, "/agents/accounts")
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, `"historySplit":["projects","tasks"]`) || !strings.Contains(body, `"historySplit":[]`) {
		t.Fatalf("%d %s", rec.Code, body)
	}
}

func TestSplitHistoryRefusalIsAConflictWithTheRecoveryMessage(t *testing.T) {
	err := &ports.SplitHistoryError{Home: "/accounts/b", Dirs: []string{"projects"}}
	rec := serveAccounts(splitAccountsService{err: err}, http.MethodPost, "/agents/accounts/b/default")
	body := rec.Body.String()
	if rec.Code != http.StatusConflict || !strings.Contains(body, "AGENT_ACCOUNT_SPLIT_HISTORY") ||
		!strings.Contains(body, "/accounts/b") || !strings.Contains(body, "move the contents") {
		t.Fatalf("%d %s", rec.Code, body)
	}
}
