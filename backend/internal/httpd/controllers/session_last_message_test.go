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
	"time"

	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd"
)

type fakeLastMessages map[domain.SessionID]string

func (f fakeLastMessages) LastAssistantMessage(_ context.Context, id domain.SessionID) (string, time.Time, bool, error) {
	text, ok := f[id]
	return text, time.Date(2026, 10, 25, 20, 11, 38, 0, time.UTC), ok, nil
}

// The latest turn's final message is part of the session detail read only;
// the list payload stays lean.
func TestSessionDetailCarriesLastAssistantMessage(t *testing.T) {
	svc := newFakeSessionService()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	deps := httpd.APIDeps{Sessions: svc, LastMessages: fakeLastMessages{"tp-1": "EVAL-COMPLETE"}}
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, deps, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	body, status, _ := doRequest(t, srv, http.MethodGet, "/api/v1/sessions/tp-1", "")
	if status != http.StatusOK {
		t.Fatalf("GET session = %d; body=%s", status, body)
	}
	var resp struct {
		LastAssistantMessage *struct {
			Text string    `json:"text"`
			At   time.Time `json:"at"`
		} `json:"lastAssistantMessage"`
	}
	if err := json.Unmarshal(body, &resp); err != nil || resp.LastAssistantMessage == nil ||
		resp.LastAssistantMessage.Text != "EVAL-COMPLETE" || resp.LastAssistantMessage.At.IsZero() {
		t.Fatalf("detail body = %s (err %v)", body, err)
	}

	body, status, _ = doRequest(t, srv, http.MethodGet, "/api/v1/sessions", "")
	if status != http.StatusOK || strings.Contains(string(body), "lastAssistantMessage") || strings.Contains(string(body), "EVAL-COMPLETE") {
		t.Fatalf("list = %d, must not carry the last message; body=%s", status, body)
	}
}

type fakeModels map[domain.SessionID]string

func (f fakeModels) EffectiveModel(_ context.Context, id domain.SessionID) string { return f[id] }

// A session spawned without a model shows the effective one on the detail read.
func TestSessionDetailFillsEffectiveModel(t *testing.T) {
	svc := newFakeSessionService()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	deps := httpd.APIDeps{Sessions: svc, Models: fakeModels{"tp-1": "provider-default"}}
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, deps, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	body, status, _ := doRequest(t, srv, http.MethodGet, "/api/v1/sessions/tp-1", "")
	var resp struct {
		Session struct {
			Model string `json:"model"`
		} `json:"session"`
	}
	if status != http.StatusOK || json.Unmarshal(body, &resp) != nil || resp.Session.Model != "provider-default" {
		t.Fatalf("status=%d model=%q body=%s", status, resp.Session.Model, body)
	}
}
