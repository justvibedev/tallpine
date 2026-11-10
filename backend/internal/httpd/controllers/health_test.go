package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
	"tallpine.eu/tallpine/backend/internal/peeradmit"
	healthsvc "tallpine.eu/tallpine/backend/internal/service/health"
)

type fakeHealth struct {
	st healthsvc.Status
	// byProject, when set, is what StatusForProject returns per project.
	byProject map[domain.ProjectID]healthsvc.Status
	err       error
}

func (f fakeHealth) Status(context.Context) (healthsvc.Status, error) { return f.st, f.err }
func (f fakeHealth) StatusForProject(_ context.Context, p domain.ProjectID) (healthsvc.Status, error) {
	if f.byProject != nil {
		return f.byProject[p], f.err
	}
	return healthsvc.Status{}, f.err
}

func serveHealth(svc HealthService) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	(&HealthController{Svc: svc}).Register(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health/silent-failures", nil))
	return rec
}

func TestHealthStatusReportsEveryCondition(t *testing.T) {
	due := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
	rec := serveHealth(fakeHealth{st: healthsvc.Status{
		ChatNotAttached: []healthsvc.ChatIssue{{SessionID: "mer-1"}},
		ReportBatches:   []healthsvc.ReportIssue{{ProjectID: "mer", Pending: 2, Claimed: 1, Undeliverable: true, Since: due}},
		TurnFallbacks:   []healthsvc.FallbackIssue{{ID: "f", SessionID: domain.SessionID("mer-2"), DueAt: due}},
	}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var got SilentFailuresResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Healthy || len(got.ChatNotAttached) != 1 || got.ChatNotAttached[0].SessionID != "mer-1" ||
		len(got.ReportBatches) != 1 || got.ReportBatches[0].Pending != 2 || !got.ReportBatches[0].Undeliverable ||
		len(got.TurnFallbacks) != 1 || got.TurnFallbacks[0].SessionID != "mer-2" || got.TurnFallbacks[0].DueAt == "" {
		t.Fatalf("response = %+v", got)
	}
}

func TestHealthStatusHealthyEncodesEmptyArrays(t *testing.T) {
	rec := serveHealth(fakeHealth{st: healthsvc.Status{}})
	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"chatNotAttached", "reportBatches", "turnFallbacks"} {
		if a, ok := raw[k].([]any); !ok || len(a) != 0 {
			t.Fatalf("%s = %v, want []", k, raw[k])
		}
	}
	if raw["healthy"] != true {
		t.Fatalf("healthy = %v", raw["healthy"])
	}
}

func TestHealthStatusErrorAndUnwired(t *testing.T) {
	if rec := serveHealth(fakeHealth{err: errors.New("db")}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("error status = %d", rec.Code)
	}
	if rec := serveHealth(nil); rec.Code != http.StatusNotImplemented {
		t.Fatalf("unwired status = %d", rec.Code)
	}
}

func TestHealthStatusScopedAgentSeesOnlyItsProject(t *testing.T) {
	svc := fakeHealth{
		st: healthsvc.Status{ReportBatches: []healthsvc.ReportIssue{{ProjectID: "a"}, {ProjectID: "b"}}},
		byProject: map[domain.ProjectID]healthsvc.Status{
			"a": {ReportBatches: []healthsvc.ReportIssue{{ProjectID: "a"}}},
		},
	}
	r := chi.NewRouter()
	(&HealthController{Svc: svc}).Register(r)
	req := httptest.NewRequest(http.MethodGet, "/health/silent-failures", nil)
	req = req.WithContext(requestscope.WithAgent(req.Context(), requestscope.Agent{Role: requestscope.RoleOrchestrator, SessionID: "a-orch", ProjectID: "a"}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var got SilentFailuresResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || len(got.ReportBatches) != 1 || got.ReportBatches[0].ProjectID != "a" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestHealthPeerCheckOnlyForFullAuthorityCallers(t *testing.T) {
	checker := peeradmit.New(peeradmit.Options{Mode: peeradmit.ModeOff, ModeReason: "unsigned or development build"})
	r := chi.NewRouter()
	(&HealthController{Svc: fakeHealth{}, PeerCheck: checker}).Register(r)

	serve := func(req *http.Request) SilentFailuresResponse {
		t.Helper()
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		var got SilentFailuresResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	full := serve(httptest.NewRequest(http.MethodGet, "/health/silent-failures", nil))
	if full.PeerCheck == nil || full.PeerCheck.Mode != "off" || full.PeerCheck.ModeReason == "" {
		t.Fatalf("full-authority report = %+v", full.PeerCheck)
	}
	req := httptest.NewRequest(http.MethodGet, "/health/silent-failures", nil)
	req = req.WithContext(requestscope.WithAgent(req.Context(), requestscope.Agent{Role: requestscope.RoleOrchestrator, ProjectID: "p"}))
	if got := serve(req); got.PeerCheck != nil {
		t.Fatalf("scoped callers must not see daemon-wide records: %+v", got.PeerCheck)
	}
}
