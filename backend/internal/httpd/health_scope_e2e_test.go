package httpd

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/daemontoken"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	healthsvc "tallpine.eu/tallpine/backend/internal/service/health"
)

type fakeHealthSource struct{}

func (fakeHealthSource) ListChatSessionsExpectedLive(context.Context, time.Time) ([]domain.SessionID, error) {
	return []domain.SessionID{"app-1", "web-1"}, nil
}
func (fakeHealthSource) ListStuckReportBatches(context.Context, time.Time) ([]domain.StuckReportBatch, error) {
	return []domain.StuckReportBatch{{ProjectID: "app", Pending: 1}, {ProjectID: "web", Pending: 2}}, nil
}
func (fakeHealthSource) ListOverdueTurnFallbacks(context.Context, time.Time) ([]domain.OverdueTurnFallback, error) {
	return []domain.OverdueTurnFallback{{ID: "fa", SessionID: "app-2"}, {ID: "fw", SessionID: "web-1"}}, nil
}

// A real orchestrator token, through the auth middleware and controller into
// the real health service, sees only its own project's items.
func TestSilentFailuresEndToEndFiltersToOrchestratorProject(t *testing.T) {
	store, tokens := newAgentScopeFixture()
	svc := &healthsvc.Service{Source: fakeHealthSource{}, Sessions: store}
	r := chi.NewRouter()
	r.Use(daemonAuthMiddleware(testDaemonToken, "", nil, newAgentScopes(tokens, store, slog.Default()), nil))
	r.Route("/api/v1", func(api chi.Router) { (&controllers.HealthController{Svc: svc}).Register(api) })

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health/silent-failures", http.NoBody)
	req.Header.Set(daemontoken.Header, store.tokenFor(tokens, "app-orch"))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var got controllers.SilentFailuresResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.ReportBatches) != 1 || got.ReportBatches[0].ProjectID != "app" ||
		len(got.ChatNotAttached) != 1 || got.ChatNotAttached[0].SessionID != "app-1" ||
		len(got.TurnFallbacks) != 1 || got.TurnFallbacks[0].ID != "fa" {
		t.Fatalf("body leaked or dropped items: %s", rec.Body)
	}
}
