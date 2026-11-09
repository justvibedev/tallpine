package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ghaccount"
)

type fakeAccountChecker struct{ found bool }

func (f fakeAccountChecker) Accounts(context.Context, domain.ProjectID) (ghaccount.AccountList, bool, error) {
	return ghaccount.AccountList{}, f.found, nil
}

func (f fakeAccountChecker) CheckProject(_ context.Context, id domain.ProjectID) (ghaccount.Inspection, string, string, bool, error) {
	return ghaccount.Inspection{Status: ghaccount.StatusMismatch, Login: "acme", TokenLogin: "justvibedev"}, "acme/repo", "acme", f.found, nil
}

func TestProjectGitHubAccountCheckReportsTokenLogin(t *testing.T) {
	get := func(c GitHubAccountChecker) *httptest.ResponseRecorder {
		r := chi.NewRouter()
		(&ProjectGitHubAccountController{Checker: c}).Register(r)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/projects/p/github-account-check", http.NoBody))
		return rec
	}
	rec := get(fakeAccountChecker{found: true})
	var body ProjectGitHubAccountCheckResponse
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body.Status != "mismatch" || body.TokenLogin != "justvibedev" || body.Override != "acme" {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "token\"") {
		t.Fatalf("response must not carry a token: %s", rec.Body)
	}
	if rec := get(fakeAccountChecker{}); rec.Code != 404 {
		t.Fatalf("unknown project code=%d", rec.Code)
	}
	if rec := get(nil); rec.Code != 501 {
		t.Fatalf("nil checker code=%d", rec.Code)
	}
}

type fakeAccountLister struct {
	res   ghaccount.AccountList
	found bool
	id    domain.ProjectID
}

func (f *fakeAccountLister) CheckProject(context.Context, domain.ProjectID) (ghaccount.Inspection, string, string, bool, error) {
	return ghaccount.Inspection{}, "", "", false, nil
}

func (f *fakeAccountLister) Accounts(_ context.Context, id domain.ProjectID) (ghaccount.AccountList, bool, error) {
	f.id = id
	return f.res, f.found, nil
}

func TestGitHubAccountsListsLoginsOnly(t *testing.T) {
	l := &fakeAccountLister{found: true, res: ghaccount.AccountList{
		Accounts: []ghaccount.Account{{Login: "alice", Push: "yes"}, {Login: "bob", Push: "no"}},
		Repo:     "acme/app", Override: "bob", AutoPick: "alice",
	}}
	r := chi.NewRouter()
	(&ProjectGitHubAccountController{Checker: l}).Register(r)
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, http.NoBody))
		return rec
	}
	rec := get("/projects/app/github-accounts")
	var body GitHubAccountsResponse
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &body) != nil || l.id != "app" ||
		len(body.Accounts) != 2 || body.Accounts[1] != (GitHubAccountItem{Login: "bob", CanPush: "no"}) ||
		body.Override != "bob" || body.AutoPick != "alice" || body.Repo != "acme/app" {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	if strings.Contains(strings.ToLower(rec.Body.String()), "token") {
		t.Fatalf("response must not mention a token: %s", rec.Body)
	}
	if rec := get("/github/accounts"); rec.Code != 200 || l.id != "" {
		t.Fatalf("machine-wide list: code=%d project=%q", rec.Code, l.id)
	}
	l.found = false
	if rec := get("/projects/nope/github-accounts"); rec.Code != 404 {
		t.Fatalf("unknown project code=%d", rec.Code)
	}
	if rec := (func() *httptest.ResponseRecorder {
		rr := chi.NewRouter()
		(&ProjectGitHubAccountController{}).Register(rr)
		rec := httptest.NewRecorder()
		rr.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/github/accounts", http.NoBody))
		return rec
	})(); rec.Code != 501 {
		t.Fatalf("nil lister code=%d", rec.Code)
	}
}
