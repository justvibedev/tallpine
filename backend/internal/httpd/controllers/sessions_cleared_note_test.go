package controllers_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
)

// The daemon wires the note from its Chat service; losing the method would
// silently answer 501 and leave a Terminal UI /clear without its brief.
var _ controllers.ClearedContextNoter = (*chatsvc.Service)(nil)

type clearedNoteStub struct {
	*fakeChatService
	note    string
	err     error
	session domain.SessionID
}

func (s *clearedNoteStub) ClearedContextNote(_ context.Context, id domain.SessionID) (string, error) {
	s.session = id
	return s.note, s.err
}

func clearedNoteServer(t *testing.T, svc controllers.ConversationService) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil,
		httpd.APIDeps{Conversations: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func TestClearedContextNoteRoute(t *testing.T) {
	svc := &clearedNoteStub{fakeChatService: &fakeChatService{}, note: "the brief"}
	body, status, headers := doRequest(t, clearedNoteServer(t, svc), http.MethodGet, "/api/v1/sessions/tp-1/cleared-context-note", "")
	assertJSON(t, headers)
	var response controllers.ClearedContextNoteResponse
	mustJSON(t, body, &response)
	if status != http.StatusOK || response.Note != "the brief" || svc.session != "tp-1" {
		t.Fatalf("note route: status=%d response=%+v session=%s", status, response, svc.session)
	}

	for _, tt := range []struct {
		err    error
		status int
	}{
		{ports.ErrSessionNotFound, http.StatusNotFound},
		{errors.New("store down"), http.StatusInternalServerError},
	} {
		svc := &clearedNoteStub{fakeChatService: &fakeChatService{}, err: tt.err}
		if _, status, _ := doRequest(t, clearedNoteServer(t, svc), http.MethodGet, "/api/v1/sessions/tp-1/cleared-context-note", ""); status != tt.status {
			t.Fatalf("%v: status=%d, want %d", tt.err, status, tt.status)
		}
	}
	if _, status, _ := doRequest(t, clearedNoteServer(t, &fakeChatService{}), http.MethodGet, "/api/v1/sessions/tp-1/cleared-context-note", ""); status != http.StatusNotImplemented {
		t.Fatalf("no note source: status=%d, want 501", status)
	}
}
