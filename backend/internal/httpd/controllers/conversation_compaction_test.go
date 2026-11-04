package controllers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
)

func (f *fakeConversationService) ContextBreakdown(context.Context, domain.SessionID) (domain.ContextBreakdown, bool, error) {
	f.breakdownCalls++
	return f.breakdown, f.breakdownOK, nil
}

func (f *fakeConversationService) RestartChatAgent(_ context.Context, id domain.SessionID) error {
	f.restarted = append(f.restarted, id)
	return f.restartErr
}

func patchSettings(t *testing.T, base, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPatch, base+"/api/v1/sessions/s1/conversation/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		out.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return resp.StatusCode, out.String()
}

// Any whole percent from 20 to 95 is a valid per-session window; anything else
// is refused before it reaches the chat.
func TestSessionCompactionWindowIsValidatedOnTheServer(t *testing.T) {
	svc := &fakeConversationService{}
	server := conversationTestServer(t, svc)
	for _, window := range []string{"", "200k", "1m", "20%", "40%", "95%"} {
		if code, body := patchSettings(t, server.URL, `{"autoCompactWindow":"`+window+`"}`); code != http.StatusOK {
			t.Fatalf("%q status = %d %s, want 200", window, code, body)
		}
	}
	accepted := len(svc.turnSettings)
	for _, window := range []string{"19%", "96%", "100%", "0%", "abc", "40", "-40%"} {
		code, body := patchSettings(t, server.URL, `{"autoCompactWindow":"`+window+`"}`)
		if code != http.StatusBadRequest || !strings.Contains(body, "CHAT_COMPACT_WINDOW_INVALID") {
			t.Fatalf("%q status = %d %s, want 400 CHAT_COMPACT_WINDOW_INVALID", window, code, body)
		}
	}
	if len(svc.turnSettings) != accepted {
		t.Fatalf("an invalid window reached the chat service: %d calls", len(svc.turnSettings)-accepted)
	}
}

func TestProviderSwitchRefusesAnInvalidCompactionWindow(t *testing.T) {
	svc := &fakeConversationService{}
	server := conversationTestServer(t, svc)
	post := func(window string) int {
		body := `{"targetHarness":"claude-code","autoCompactWindow":"` + window + `"}`
		resp, err := http.Post(server.URL+"/api/v1/sessions/s1/provider-switch", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := post("96%"); code != http.StatusBadRequest {
		t.Fatalf("96%% status = %d, want 400", code)
	}
	if code := post("40%"); code == http.StatusBadRequest {
		t.Fatalf("40%% was refused")
	}
}

// The popover shows "applies after restart" from this flag.
func TestTurnSettingsResponseCarriesThePendingRestartFlag(t *testing.T) {
	svc := &fakeConversationService{compactionPending: true, projectTokens: 250000}
	server := conversationTestServer(t, svc)
	_, body := patchSettings(t, server.URL, `{"autoCompactWindow":"40%"}`)
	var got struct {
		AutoCompactWindow  string `json:"autoCompactWindow"`
		AutoCompactPending bool   `json:"autoCompactPending"`
		ProjectTokens      int    `json:"autoCompactProjectTokens"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil || got.AutoCompactWindow != "40%" || !got.AutoCompactPending || got.ProjectTokens != 250000 {
		t.Fatalf("response = %s (%v), want 40%% and autoCompactPending", body, err)
	}
}

func TestRestartAgentKeepsTheConversationAndRefusesABusyAgent(t *testing.T) {
	svc := &fakeConversationService{}
	server := conversationTestServer(t, svc)
	post := func() int {
		resp, err := http.Post(server.URL+"/api/v1/sessions/s1/conversation/restart-agent", "application/json", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := post(); code != http.StatusNoContent || len(svc.restarted) != 1 || svc.restarted[0] != "s1" {
		t.Fatalf("restart: status %d restarted %v", code, svc.restarted)
	}
	svc.restartErr = chatsvc.ErrRestartBusy
	if code := post(); code != http.StatusConflict {
		t.Fatalf("busy agent status = %d, want 409", code)
	}
}

func getBreakdown(t *testing.T, base string) (int, ContextBreakdownJSON) {
	t.Helper()
	resp, err := http.Get(base + "/api/v1/sessions/s1/conversation/context-breakdown")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out ContextBreakdownJSON
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// ContextBreakdownJSON is the wire shape a client reads.
type ContextBreakdownJSON struct {
	Available            bool  `json:"available"`
	TotalTokens          int64 `json:"totalTokens"`
	MaxTokens            int64 `json:"maxTokens"`
	AutoCompactThreshold int64 `json:"autoCompactThreshold"`
	Categories           []struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Tokens int64  `json:"tokens"`
		Kind   string `json:"kind"`
	} `json:"categories"`
	Messages *struct {
		ToolResults int64 `json:"toolResults"`
	} `json:"messages"`
}

func TestContextBreakdownEndpointReturnsTheAgentsCategoriesOnRequest(t *testing.T) {
	svc := &fakeConversationService{breakdownOK: true, breakdown: domain.ContextBreakdown{
		TotalTokens: 50000, MaxTokens: 200000, AutoCompactThreshold: 140000,
		Categories: []domain.ContextCategory{{ID: "messages", Name: "Messages", Tokens: 30000, Kind: domain.ContextKindUsed}},
		Messages:   &domain.ContextMessageParts{ToolResults: 20000},
	}}
	server := conversationTestServer(t, svc)
	if svc.breakdownCalls != 0 {
		t.Fatal("the breakdown was requested without anyone asking")
	}
	code, got := getBreakdown(t, server.URL)
	if code != http.StatusOK || !got.Available || got.TotalTokens != 50000 || got.AutoCompactThreshold != 140000 ||
		len(got.Categories) != 1 || got.Categories[0].Kind != "used" || got.Messages == nil || got.Messages.ToolResults != 20000 {
		t.Fatalf("status %d body %+v", code, got)
	}
}

func TestContextBreakdownEndpointSaysUnavailableInsteadOfInventingNumbers(t *testing.T) {
	svc := &fakeConversationService{breakdownOK: false, breakdown: domain.ContextBreakdown{TotalTokens: 99, MaxTokens: 100}}
	code, got := getBreakdown(t, conversationTestServer(t, svc).URL)
	if code != http.StatusOK || got.Available || got.TotalTokens != 0 || len(got.Categories) != 0 {
		t.Fatalf("status %d body %+v; want available=false and nothing else", code, got)
	}
}
