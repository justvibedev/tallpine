package controllers_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd"
)

type fakeIntents struct {
	added []domain.OrchestratorIntent
	all   bool
}

func (f *fakeIntents) InsertOrchestratorIntent(_ context.Context, in domain.OrchestratorIntent) error {
	f.added = append(f.added, in)
	return nil
}
func (f *fakeIntents) SelectOrchestratorIntents(_ context.Context, _ domain.ProjectID, all bool) ([]domain.OrchestratorIntent, error) {
	f.all = all
	return f.added, nil
}
func (f *fakeIntents) MarkOrchestratorIntentDone(_ context.Context, _ domain.ProjectID, id string, _ time.Time) (bool, error) {
	return id == "known", nil
}

func TestIntentsRoutes(t *testing.T) {
	f := &fakeIntents{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{AllowedOrigins: []string{"app://renderer"}}, log, nil, httpd.APIDeps{Intents: f}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	_, st, _ := doRequest(t, srv, http.MethodPost, "/api/v1/projects/tp/intents", `{"text":"  hold install "}`)
	if st != http.StatusCreated || len(f.added) != 1 || f.added[0].Text != "hold install" {
		t.Fatalf("create st=%d %+v", st, f.added)
	}
	_, st, _ = doRequest(t, srv, http.MethodPost, "/api/v1/projects/tp/intents", `{"text":"  "}`)
	if st != http.StatusBadRequest {
		t.Fatalf("blank st=%d", st)
	}
	raw, st, _ := doRequest(t, srv, http.MethodGet, "/api/v1/projects/tp/intents?all=true", "")
	if st != http.StatusOK || !f.all || !strings.Contains(string(raw), "hold install") {
		t.Fatalf("list st=%d all=%v %s", st, f.all, raw)
	}
	if _, st, _ = doRequest(t, srv, http.MethodPost, "/api/v1/projects/tp/intents/known/done", ""); st != http.StatusNoContent {
		t.Fatalf("done st=%d", st)
	}
	if _, st, _ = doRequest(t, srv, http.MethodPost, "/api/v1/projects/tp/intents/nope/done", ""); st != http.StatusNotFound {
		t.Fatalf("missing st=%d", st)
	}
}
