package controllers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/service/githubcred"
)

type fakeCredSvc struct {
	cred githubcred.Credential
	err  error
	got  struct {
		session domain.SessionID
		host    string
	}
}

func (f *fakeCredSvc) ProjectGitEnv(context.Context, domain.ProjectID) []string { return nil }

func (f *fakeCredSvc) Issue(_ context.Context, id domain.SessionID, host string) (githubcred.Credential, error) {
	f.got.session, f.got.host = id, host
	return f.cred, f.err
}

func postCred(svc controllers.GitHubCredentialService, body string) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	(&controllers.GitHubCredentialController{Svc: svc}).Register(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/sessions/p-1/github-credential", strings.NewReader(body)))
	return rec
}

func TestGitHubCredential_IssuesAndNeverCaches(t *testing.T) {
	svc := &fakeCredSvc{cred: githubcred.Credential{Username: "x-access-token", Token: "tok", Source: githubcred.SourceAccount}}
	rec := postCred(svc, `{"host":"github.com","protocol":"https"}`)
	var got controllers.GitHubCredentialResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusOK || got.Token != "tok" || got.Source != "account" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("Cache-Control") != "no-store" || svc.got.session != "p-1" || svc.got.host != "github.com" {
		t.Fatalf("a credential must not be cacheable and must be asked for the path's session: %v %+v", rec.Header(), svc.got)
	}
}

func TestGitHubCredential_RefusalsMapToDistinctCodes(t *testing.T) {
	for err, want := range map[error][2]string{
		githubcred.ErrDenied:          {"403", "GITHUB_CREDENTIALS_DENIED"},
		githubcred.ErrHostNotAllowed:  {"403", "GITHUB_HOST_NOT_ALLOWED"},
		githubcred.ErrUnavailable:     {"404", "GITHUB_CREDENTIAL_UNAVAILABLE"},
		githubcred.ErrSessionNotFound: {"404", "SESSION_NOT_FOUND"},
	} {
		rec := postCred(&fakeCredSvc{err: err}, `{"host":"github.com"}`)
		if !strings.Contains(rec.Body.String(), want[1]) || rec.Code != map[string]int{"403": 403, "404": 404}[want[0]] || strings.Contains(rec.Body.String(), "token") && strings.Contains(rec.Body.String(), "tok\"") {
			t.Errorf("%v = %d %s, want %s %s", err, rec.Code, rec.Body, want[0], want[1])
		}
	}
}
