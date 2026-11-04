package acp

import (
	"context"
	"io"
	"log/slog"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestSessionInfoUpdateEmitsContextUsageFromBindingHook(t *testing.T) {
	agent := &fakeAgent{promptNoPermission: true}
	driver := New(Config{
		Harness:      domain.HarnessClaudeCode,
		Capabilities: ports.ChatCapabilities{ports.ChatCapabilityStreaming: true},
		Probe:        func(context.Context) error { return nil },
		Launch:       func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
		InfoContextUsage: func(meta map[string]any) (int64, int64, bool) {
			v, ok := meta["pct"].(float64)
			return int64(v), 100, ok
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeSpawn(agent))
	opened := startConversation(t, driver)
	_ = nextEvent(t, opened.Events())
	if err := agent.conn.SessionUpdate(context.Background(), acpsdk.SessionNotification{
		SessionId: acpsdk.SessionId(opened.ProviderConversationID()),
		Update: acpsdk.SessionUpdate{SessionInfoUpdate: &acpsdk.SessionSessionInfoUpdate{
			SessionUpdate: "session_info_update", Meta: map[string]any{"pct": float64(42)},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	ev := nextEvent(t, opened.Events())
	if ev.Kind != ports.ChatEventUsage || ev.Usage == nil || ev.Usage.ContextUsed != 42 || ev.Usage.ContextWindow != 100 || !ev.Usage.ContextKnown {
		t.Fatalf("event = %#v", ev)
	}
	// The figure is a percentage, not tokens: compaction wording is built from
	// contextTokens and must not see it.
	conv := opened.(*conversation)
	conv.mu.Lock()
	defer conv.mu.Unlock()
	if conv.contextTokens != 0 || conv.contextWindow != 0 {
		t.Fatalf("percentage leaked into token tracking: %d/%d", conv.contextTokens, conv.contextWindow)
	}
}
