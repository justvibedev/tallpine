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
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
)

// researchFake adds the research mode ability to the conversation fake.
type researchFake struct {
	*fakeConversationService
	err     error
	on      []bool
	windows []string
}

func (f *researchFake) SetResearchWindow(_ context.Context, _ domain.SessionID, window string) (chatsvc.ResearchStatus, error) {
	f.windows = append(f.windows, window)
	if f.err != nil {
		return chatsvc.ResearchStatus{}, f.err
	}
	return chatsvc.ResearchStatus{Active: true, Available: true, Window: 400_000, ResearchWindow: window}, nil
}

func (f *researchFake) SetResearchMode(_ context.Context, _ domain.SessionID, on bool) (chatsvc.ResearchStatus, error) {
	f.on = append(f.on, on)
	if f.err != nil {
		return chatsvc.ResearchStatus{}, f.err
	}
	return chatsvc.ResearchStatus{Active: on, Available: true, Window: 250_000, ResearchWindow: "250k", Since: time.Unix(1_700_000_000, 0)}, nil
}

func (f *researchFake) ResearchStatus(context.Context, domain.SessionID) (chatsvc.ResearchStatus, bool) {
	return chatsvc.ResearchStatus{Available: true, Window: 120_000, ResearchWindow: "250k"}, true
}

// researchHandler serves the real router without binding a port.
func researchHandler(svc *researchFake) http.Handler {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{
		Sessions: newFakeSessionService(), Conversations: svc,
	}, httpd.ControlDeps{})
}

func postResearch(t *testing.T, h http.Handler, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/orchestrators/orch-1/research", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Host, req.RemoteAddr = "127.0.0.1:3101", "127.0.0.1:50000"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func TestOrchestratorResearchSwitchesAndReportsTheWindow(t *testing.T) {
	svc := &researchFake{fakeConversationService: &fakeConversationService{}}
	code, body := postResearch(t, researchHandler(svc), `{"enabled":true}`)
	var got struct {
		Active       bool   `json:"active"`
		WindowTokens int    `json:"windowTokens"`
		Since        string `json:"since"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil || code != http.StatusOK || !got.Active || got.WindowTokens != 250_000 || got.Since == "" {
		t.Fatalf("status %d body %s", code, body)
	}
	if len(svc.on) != 1 || !svc.on[0] {
		t.Fatalf("service calls = %v", svc.on)
	}
}

func TestOrchestratorResearchRefusalsAreConflictsWithAClearMessage(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"unsupported": {chatsvc.ErrResearchUnsupported, "not supported for this agent"},
		"disabled":    {chatsvc.ErrResearchDisabled, "Dynamic orchestrator context"},
	} {
		code, body := postResearch(t, researchHandler(&researchFake{fakeConversationService: &fakeConversationService{}, err: tc.err}), `{"enabled":true}`)
		if code != http.StatusConflict || !strings.Contains(body, tc.want) {
			t.Errorf("%s: status %d body %s, want 409 mentioning %q", name, code, body, tc.want)
		}
	}
	code, body := postResearch(t, researchHandler(&researchFake{fakeConversationService: &fakeConversationService{}, err: chatsvc.ErrResearchRequestFailed}), `{"enabled":true}`)
	if code != http.StatusBadGateway || !strings.Contains(body, "RESEARCH_MODE_REQUEST_FAILED") || strings.Contains(body, "keeps its compaction window") {
		t.Errorf("request failed: status %d body %s, want 502 with its own code", code, body)
	}
}

func TestOrchestratorResearchNeedsAnExplicitEnabled(t *testing.T) {
	svc := &researchFake{fakeConversationService: &fakeConversationService{}}
	code, _ := postResearch(t, researchHandler(svc), `{}`)
	if code != http.StatusBadRequest || len(svc.on) != 0 {
		t.Fatalf("status %d calls %v, want 400 and no switch", code, svc.on)
	}
}

func TestOrchestratorResearchWindowRoutesToTheWindowSwitch(t *testing.T) {
	svc := &researchFake{fakeConversationService: &fakeConversationService{}}
	h := researchHandler(svc)
	if code, body := postResearch(t, h, `{"enabled":true,"window":"400k"}`); code != http.StatusOK || !strings.Contains(body, `"researchWindow":"400k"`) {
		t.Fatalf("status %d body %s", code, body)
	}
	if len(svc.windows) != 1 || svc.windows[0] != "400k" || len(svc.on) != 0 {
		t.Fatalf("windows %v on %v, want one window switch", svc.windows, svc.on)
	}
	if code, _ := postResearch(t, h, `{"enabled":false,"window":"400k"}`); code != http.StatusBadRequest {
		t.Fatalf("off with a window: status %d, want 400", code)
	}
	svc.err = chatsvc.ErrResearchWindowTooLarge
	if code, body := postResearch(t, h, `{"enabled":true,"window":"1m"}`); code != http.StatusConflict || !strings.Contains(body, "RESEARCH_WINDOW_TOO_LARGE") {
		t.Fatalf("too large: status %d body %s", code, body)
	}
	svc.err = chatsvc.ErrResearchWindowInvalid
	if code, body := postResearch(t, h, `{"enabled":true,"window":"300k"}`); code != http.StatusBadRequest || !strings.Contains(body, "RESEARCH_WINDOW_INVALID") {
		t.Fatalf("invalid: status %d body %s", code, body)
	}
}
