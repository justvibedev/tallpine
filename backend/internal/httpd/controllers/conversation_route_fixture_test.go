package controllers_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/httpd"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
)

// newConversationHandler builds the daemon router with only the conversation
// service wired (nil leaves the Chat driver unwired).
func newConversationHandler(svc controllers.ConversationService) http.Handler {
	return httpd.NewRouterWithControl(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, httpd.APIDeps{
		Sessions:      newFakeSessionService(),
		Conversations: svc,
	}, httpd.ControlDeps{})
}

// newConversationServer serves newConversationHandler until test cleanup.
func newConversationServer(t *testing.T, svc controllers.ConversationService) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(newConversationHandler(svc))
	t.Cleanup(srv.Close)
	return srv
}
