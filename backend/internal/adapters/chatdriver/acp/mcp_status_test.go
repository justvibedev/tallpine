package acp

import (
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"unicode/utf8"

	acpsdk "github.com/coder/acp-go-sdk"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// mcpNotice mirrors (fires from system/init inside a prompt, acp-agent.js:4216;
// statuses failed|needs-auth only; disconnected is defensive) claude-agent-acp 0.99 reportMcpServerStartupFailures
// (acp-agent.js:2425-2440): a tagged agent_message_chunk.
func mcpNotice(server, status, text string) acpsdk.SessionNotification {
	return acpsdk.SessionNotification{SessionId: "s1", Update: acpsdk.SessionUpdate{
		AgentMessageChunk: &acpsdk.SessionUpdateAgentMessageChunk{
			Content: acpsdk.TextBlock(text),
			Meta:    map[string]any{"claudeCode": map[string]any{"kind": "mcpServerStatus", "server": server, "status": status}},
		},
	}}
}

func drainMCP(t *testing.T, conv *conversation, notes ...acpsdk.SessionNotification) []ports.ChatEvent {
	t.Helper()
	for _, n := range notes {
		if err := conv.processUpdate(n); err != nil {
			t.Fatal(err)
		}
	}
	close(conv.events)
	var out []ports.ChatEvent
	for e := range conv.events {
		out = append(out, e)
	}
	return out
}

func newMCPConv() *conversation {
	return &conversation{sessionID: "s1", activeTurn: "turn-1", events: make(chan ports.ChatEvent, 16),
		log: slog.New(slog.DiscardHandler), messages: map[string]string{}}
}

const failedText = `**MCP server "db" failed to start:** Its tools are unavailable in this session. Run /mcp for the error.`

func TestMCPFailedServerShowsOneItemWithNameAndError(t *testing.T) {
	events := drainMCP(t, newMCPConv(), mcpNotice("db", "failed", failedText))
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1: %+v", len(events), events)
	}
	e := events[0]
	if e.ActivityKind != domain.ActivityKindSystem || e.ActivityStatus != domain.ActivityStatusFailed ||
		!strings.Contains(e.Summary, "db") || !strings.Contains(string(e.Detail), `"status":"failed"`) ||
		strings.Contains(string(e.Detail), "/mcp") {
		t.Fatalf("bad event %+v detail=%s", e, e.Detail)
	}
}

func TestMCPNonFailureStatusEmitsNothing(t *testing.T) {
	if events := drainMCP(t, newMCPConv(), mcpNotice("db", "connected", "")); len(events) != 0 {
		t.Fatalf("success produced %+v", events)
	}
}

func TestMCPRepeatedFailureDeduped(t *testing.T) {
	events := drainMCP(t, newMCPConv(), mcpNotice("db", "failed", failedText), mcpNotice("db", "failed", failedText))
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
}

func TestMCPErrorTextRedactsSecrets(t *testing.T) {
	text := `**MCP server "db" failed to start:** spawn failed API_TOKEN=abc123secret Authorization: Bearer xyz789 sk-abcdefghijklmnopqrstuv`
	events := drainMCP(t, newMCPConv(), mcpNotice("db", "failed", text))
	if len(events) != 1 {
		t.Fatalf("events = %d", len(events))
	}
	var detail map[string]any
	_ = json.Unmarshal(events[0].Detail, &detail)
	got, _ := detail["text"].(string)
	for _, leak := range []string{"abc123secret", "xyz789", "sk-abcdefgh"} {
		if strings.Contains(got, leak) {
			t.Fatalf("leaked %q in %q", leak, got)
		}
	}
	if !strings.Contains(got, "spawn failed") {
		t.Fatalf("lost message: %q", got)
	}
}

func TestMCPErrorTextRedactsURLCredentialsAndTruncatesOnRuneBoundary(t *testing.T) {
	for _, pad := range []string{"", "a"} { // both byte parities so a byte cut splits a rune
		text := `**MCP server "db" failed to start:** connect postgres://user:hunter2@host/db ` + pad + strings.Repeat("é", 300)
		events := drainMCP(t, newMCPConv(), mcpNotice("db", "failed", text))
		var detail map[string]any
		_ = json.Unmarshal(events[0].Detail, &detail)
		got, _ := detail["text"].(string)
		if strings.Contains(got, "hunter2") || !strings.Contains(got, "postgres://") {
			t.Fatalf("url creds not redacted: %q", got)
		}
		if !utf8.ValidString(got) || strings.ContainsRune(got, utf8.RuneError) { // Marshal turns split runes into U+FFFD
			t.Fatalf("invalid utf8 (pad %q): %q", pad, got)
		}
	}
}

func TestMCPNoticeWithoutActiveTurnEmitsNothing(t *testing.T) {
	conv := newMCPConv()
	conv.activeTurn = ""
	if events := drainMCP(t, conv, mcpNotice("db", "failed", failedText)); len(events) != 0 {
		t.Fatalf("orphan activity: %+v", events)
	}
}
