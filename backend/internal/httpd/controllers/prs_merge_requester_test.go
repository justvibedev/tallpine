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
)

type ownPRScope struct{}

func (ownPRScope) GetPR(context.Context, string) (domain.PullRequest, bool, error) {
	return domain.PullRequest{SessionID: "orch-1"}, true, nil
}
func (ownPRScope) GetPRByNumber(context.Context, int) (domain.PullRequest, bool, error) {
	return domain.PullRequest{}, false, nil
}
func (ownPRScope) GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error) {
	return domain.SessionRecord{}, false, nil
}

// B560: the merge request carries the scoped agent that made it (so auto-close
// can skip the notice to that same session); app/CLI callers carry none.
func TestPRsRoutes_Merge_PassesRequester(t *testing.T) {
	for _, tc := range []struct {
		agent bool
		want  domain.SessionID
	}{{true, "orch-1"}, {false, ""}} {
		svc := &fakePRService{}
		r := chi.NewRouter()
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if tc.agent {
					req = req.WithContext(requestscope.WithAgent(req.Context(), requestscope.Agent{Role: requestscope.RoleOrchestrator, SessionID: "orch-1", ProjectID: "p1"}))
				}
				next.ServeHTTP(w, req)
			})
		})
		r.Route("/api/v1", func(r chi.Router) { (&controllers.PRsController{Svc: svc, Scope: ownPRScope{}}).Register(r) })
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/prs/42/merge", strings.NewReader(validMergeBody)))
		if rec.Code != http.StatusOK || svc.mergeRequest.Requester != tc.want {
			t.Fatalf("agent=%v: status %d requester %q, want 200 and %q", tc.agent, rec.Code, svc.mergeRequest.Requester, tc.want)
		}
	}
}

// Only the preflight route may set DryRun, and it must always set it: it is the
// only thing stopping that route from really merging.
func TestPRsRoutes_MergePreflightIsDryRunAndMergeIsNot(t *testing.T) {
	for path, wantDry := range map[string]bool{"/api/v1/prs/42/merge-preflight": true, "/api/v1/prs/42/merge": false} {
		svc := &fakePRService{}
		r := chi.NewRouter()
		r.Route("/api/v1", func(r chi.Router) { (&controllers.PRsController{Svc: svc, Scope: ownPRScope{}}).Register(r) })
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(validMergeBody)))
		if rec.Code != http.StatusOK || svc.mergeRequest.DryRun != wantDry {
			t.Fatalf("%s: status %d DryRun %v, want 200 and %v", path, rec.Code, svc.mergeRequest.DryRun, wantDry)
		}
	}
}
