package controllers_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd"
)

type fakeInstructions map[domain.SessionID]string

func (f fakeInstructions) WorkerInstructions(_ context.Context, id domain.SessionID) (string, error) {
	return f[id], nil
}

// The effective worker rules are part of the session detail read only.
func TestSessionDetailCarriesWorkerInstructions(t *testing.T) {
	svc := newFakeSessionService()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	deps := httpd.APIDeps{Sessions: svc, Instructions: fakeInstructions{"tp-1": "Run the tests."}}
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, deps, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	body, status, _ := doRequest(t, srv, http.MethodGet, "/api/v1/sessions/tp-1", "")
	var resp struct {
		WorkerInstructions string `json:"workerInstructions"`
	}
	if status != http.StatusOK || json.Unmarshal(body, &resp) != nil || resp.WorkerInstructions != "Run the tests." {
		t.Fatalf("detail = %d %s", status, body)
	}
	body, status, _ = doRequest(t, srv, http.MethodGet, "/api/v1/sessions", "")
	if status != http.StatusOK || strings.Contains(string(body), "Run the tests.") {
		t.Fatalf("list must not carry instructions: %d %s", status, body)
	}
}
