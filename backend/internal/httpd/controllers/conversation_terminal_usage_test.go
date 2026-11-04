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
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
)

type usageChatService struct {
	*fakeConversationService
}

func (usageChatService) ModelUsage(context.Context, domain.SessionID) ([]domain.ModelUsage, error) {
	return nil, chatsvc.ErrNotChatMode
}

type fakeTerminalUsage struct{ supported bool }

func (f fakeTerminalUsage) TerminalModelUsage(context.Context, domain.SessionID) ([]domain.ModelUsage, bool, error) {
	return []domain.ModelUsage{{Model: "qwen3-coder-next", Credits: 1.25, Turns: 2}}, f.supported, nil
}

func usageRouter(term controllers.TerminalUsageService) http.Handler {
	r := chi.NewRouter()
	(&controllers.ConversationsController{Svc: usageChatService{&fakeConversationService{}}, TerminalUsage: term}).Register(r)
	return r
}

func TestTerminalModeUsageReportsCredits(t *testing.T) {
	rec := httptest.NewRecorder()
	usageRouter(fakeTerminalUsage{supported: true}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sessions/w-1/usage", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"credits":1.25`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestTerminalModeUsageUnsupportedKeepsModeMismatch(t *testing.T) {
	rec := httptest.NewRecorder()
	usageRouter(fakeTerminalUsage{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sessions/w-1/usage", nil))
	if rec.Code != 409 {
		t.Fatalf("code %d", rec.Code)
	}
}
