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
