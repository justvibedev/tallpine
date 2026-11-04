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
	"tallpine.eu/tallpine/backend/internal/service/accountadopt"
)

type fakeAdopt struct {
	controllers.AccountAdoptService
	replay bool
	err    error
	calls  int
}

func (f *fakeAdopt) Adopt(_ context.Context, id string, p domain.AccountAdoptionPolicy, key string) (domain.AccountAdoption, bool, error) {
	f.calls++
	return domain.AccountAdoption{ID: "b1", AccountID: id, Policy: p, State: domain.AccountAdoptionRunning,
		Items: []domain.AccountAdoptionItem{{SessionID: "s1", State: domain.AccountItemWaiting}}}, f.replay, f.err
}

func serveAdopt(svc controllers.AccountAdoptService, body string) *httptest.ResponseRecorder {
	router := chi.NewRouter()
	(&controllers.AccountAdoptController{Svc: svc}).Register(router)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/agents/accounts/acct-b/adopt", strings.NewReader(body)))
	return rec
}

func TestAdoptStatusCodes(t *testing.T) {
	const body = `{"policy":"wait","requestKey":"k"}`
	if rec := serveAdopt(&fakeAdopt{}, body); rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"state":"waiting"`) {
		t.Fatalf("new batch: %d %s", rec.Code, rec.Body.String())
	}
	if rec := serveAdopt(&fakeAdopt{replay: true}, body); rec.Code != http.StatusOK {
		t.Fatalf("replay: %d", rec.Code)
	}
	busy := &fakeAdopt{err: &accountadopt.InProgressError{Batch: domain.AccountAdoption{ID: "running-1"}}}
	if rec := serveAdopt(busy, body); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "running-1") || !strings.Contains(rec.Body.String(), "ADOPTION_IN_PROGRESS") {
		t.Fatalf("active batch: %d %s", rec.Code, rec.Body.String())
	}
	if rec := serveAdopt(&fakeAdopt{err: accountadopt.ErrUnsupportedProvider}, body); rec.Code != http.StatusBadRequest {
		t.Fatalf("codex: %d", rec.Code)
	}
	if rec := serveAdopt(nil, body); rec.Code != http.StatusNotImplemented {
		t.Fatalf("no service: %d", rec.Code)
	}
}
