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
	sessionsvc "tallpine.eu/tallpine/backend/internal/service/session"
)

// ctxService records the agent lane request each lane call carried.
type ctxService struct {
	*fakeSessionService
	end, brief sessionsvc.AgentLaneRequest
}

func (s *ctxService) EndLane(ctx context.Context, id domain.SessionID, dry bool) (sessionsvc.EndLaneOutcome, error) {
	s.end = sessionsvc.AgentLaneOf(ctx)
	return s.fakeSessionService.EndLane(ctx, id, dry)
}

func (s *ctxService) SpawnLaneOrchestratorBrief(ctx context.Context, p domain.ProjectID, clean bool, m domain.SessionMode, a domain.PermissionMode, lane, prompt string) (domain.Session, bool, error) {
	s.brief = sessionsvc.AgentLaneOf(ctx)
	return s.fakeSessionService.SpawnLaneOrchestratorBrief(ctx, p, clean, m, a, lane, prompt)
}

// An agent's lane end must ask the service to refuse open PRs; the owner's must not.
// An agent's lane start records itself as starter and carries the model.
func TestAgentLaneRequestsReachTheService(t *testing.T) {
	svc := &ctxService{fakeSessionService: newFakeSessionService()}
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.Header.Get("X-Agent") != "" {
				a := requestscope.Agent{Role: requestscope.RoleOrchestrator, SessionID: "tp-orch", ProjectID: "tp"}
				req = req.WithContext(requestscope.WithAgent(req.Context(), a))
			}
			next.ServeHTTP(w, req)
		})
	})
	r.Route("/api/v1", (&controllers.SessionsController{Svc: svc}).Register)
	do := func(agent bool, path, body string) int {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		if agent {
			req.Header.Set("X-Agent", "1")
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}
	if c := do(true, "/api/v1/orchestrators/end", `{"sessionId":"tp-lane"}`); c != 200 || !svc.end.RefuseOpenPR {
		t.Fatalf("agent end: %d %+v", c, svc.end)
	}
	if c := do(false, "/api/v1/orchestrators/end", `{"sessionId":"tp-lane"}`); c != 200 || svc.end.RefuseOpenPR {
		t.Fatalf("owner end: %d %+v", c, svc.end)
	}
}
