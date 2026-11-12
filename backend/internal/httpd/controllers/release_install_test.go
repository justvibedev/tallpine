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
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
	"tallpine.eu/tallpine/backend/internal/releaseinstall"
)

type fakeReleaseInstaller struct {
	calls []string
	err   error
}

func (f *fakeReleaseInstaller) Install(_ context.Context, sha string) (releaseinstall.Result, error) {
	f.calls = append(f.calls, sha)
	return releaseinstall.Result{SHA: sha, App: "/app", Log: "/log", ResultFile: "/log.result", PID: 7}, f.err
}

func postRelease(t *testing.T, svc controllers.ReleaseInstaller, role requestscope.AgentRole, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if role != "" {
				req = req.WithContext(requestscope.WithAgent(req.Context(), requestscope.Agent{Role: role, SessionID: domain.SessionID("s-1"), ProjectID: "p1"}))
			}
			next.ServeHTTP(w, req)
		})
	})
	r.Route("/api/v1", func(r chi.Router) { (&controllers.ReleaseInstallController{Svc: svc}).Register(r) })
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/release/install", strings.NewReader(body)))
	return rec
}

func TestReleaseInstallRoleGate(t *testing.T) {
	const body = `{"sha":"0123456789abcdef0123456789abcdef01234567"}`
	for _, role := range []requestscope.AgentRole{requestscope.RoleWorker, requestscope.RoleReviewer} {
		f := &fakeReleaseInstaller{}
		if rec := postRelease(t, f, role, body); rec.Code != http.StatusForbidden || len(f.calls) != 0 {
			t.Errorf("%s: status %d, calls %v; want 403 and no install", role, rec.Code, f.calls)
		}
	}
	f := &fakeReleaseInstaller{}
	rec := postRelease(t, f, requestscope.RoleOrchestrator, body)
	if rec.Code != http.StatusAccepted || len(f.calls) != 1 || !strings.Contains(rec.Body.String(), `"result":"/log.result"`) {
		t.Fatalf("orchestrator: status %d, calls %v, body %s", rec.Code, f.calls, rec.Body.String())
	}
}

func TestReleaseInstallRefusalIsForbidden(t *testing.T) {
	f := &fakeReleaseInstaller{err: releaseinstall.ErrRefused}
	rec := postRelease(t, f, requestscope.RoleOrchestrator, `{"sha":"x"}`)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "RELEASE_INSTALL_REFUSED") {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
}

type fakeBuildRequester struct {
	projects []string
	err      error
}

func (f *fakeBuildRequester) Request(_ context.Context, projectID string) (releaseinstall.Result, error) {
	f.projects = append(f.projects, projectID)
	return releaseinstall.Result{Log: "/log", PID: 9}, f.err
}

func postBuild(t *testing.T, build controllers.ReleaseBuildRequester, role requestscope.AgentRole) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if role != "" {
				req = req.WithContext(requestscope.WithAgent(req.Context(), requestscope.Agent{Role: role, SessionID: domain.SessionID("s-1"), ProjectID: "p1"}))
			}
			next.ServeHTTP(w, req)
		})
	})
	r.Route("/api/v1", func(r chi.Router) { (&controllers.ReleaseInstallController{Build: build}).Register(r) })
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/release/request-build", strings.NewReader("{}")))
	return rec
}

func TestReleaseRequestBuild(t *testing.T) {
	f := &fakeBuildRequester{}
	rec := postBuild(t, f, requestscope.RoleWorker)
	if rec.Code != http.StatusAccepted || len(f.projects) != 1 || f.projects[0] != "p1" || !strings.Contains(rec.Body.String(), `"pid":9`) || !strings.Contains(rec.Body.String(), `"log":"/log"`) {
		t.Fatalf("worker: status %d projects %v body %s", rec.Code, f.projects, rec.Body.String())
	}
	f = &fakeBuildRequester{}
	if rec := postBuild(t, f, ""); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "NO_PROJECT") || len(f.projects) != 0 {
		t.Fatalf("unscoped: status %d body %s", rec.Code, rec.Body.String())
	}
	f = &fakeBuildRequester{err: releaseinstall.ErrRefused}
	if rec := postBuild(t, f, requestscope.RoleWorker); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "RELEASE_BUILD_REFUSED") {
		t.Fatalf("refused: status %d body %s", rec.Code, rec.Body.String())
	}
	if rec := postBuild(t, nil, requestscope.RoleWorker); rec.Code != http.StatusNotImplemented {
		t.Fatalf("nil Build: status %d, want 501", rec.Code)
	}
}
