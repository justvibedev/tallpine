package acp

import (
	"context"
	"log/slog"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// A turn that started before any assistant message (e.g. /clear) is asked about
// with its own start time, and a binding that finds nothing emits no reroute.
func TestFinishPromptPassesTurnStartToServedModel(t *testing.T) {
	var since time.Time
	conv := &conversation{
		sessionID: "session-1", activeTurn: "turn-1",
		events: make(chan ports.ChatEvent, 16), log: slog.New(slog.DiscardHandler),
		turnModelID: "turn-1", turnModel: "fable", turnStart: time.Now(),
		servedModel: func(_ context.Context, _, _ string, s time.Time) (string, string) {
			since = s
			return "", ""
		},
	}
	want := conv.turnStart
	conv.finishPrompt("turn-1", acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil)
	if !since.Equal(want) {
		t.Fatalf("servedModel since = %v, want turn start %v", since, want)
	}
	for len(conv.events) > 0 {
		if ev := <-conv.events; ev.Kind == ports.ChatEventModelRerouted {
			t.Fatal("reroute emitted for a turn with no assistant message")
		}
	}
}

// Compaction starts a turn outside SendTurn; it must stamp its own start so the
// served-model lookup is not bounded by the previous prompt turn's start.
func TestCompactTurnStampsItsOwnStartForServedModel(t *testing.T) {
	agent := &fakeAgent{}
	sinceCh := make(chan time.Time, 4)
	cfg := fakeConfig(ports.ChatCapabilityCompaction)
	cfg.ServedModel = func(_ context.Context, _ LaunchConfig, _, _ string, since time.Time) (string, string) {
		sinceCh <- since
		return "", ""
	}
	opened := startConversation(t, newDriverWith(cfg, agent))
	_ = nextEvent(t, opened.Events())
	agent.mu.Lock()
	agent.customPrompt = func(context.Context, acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
		return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
	}
	agent.mu.Unlock()
	conv := opened.(*conversation)
	stale := time.Now().Add(-time.Hour)
	conv.mu.Lock()
	conv.turnStart = stale
	conv.configOptions = []ports.ChatConfigOption{{
		ID: "model", Type: ports.ChatConfigOptionSelect,
		Current: ports.ChatConfigOptionValue{Select: "fable"},
	}}
	conv.mu.Unlock()
	if _, err := conv.Compact(context.Background()); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	select {
	case since := <-sinceCh:
		if !since.After(stale) {
			t.Fatalf("served-model since = %v, want the compaction's own start", since)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("served-model lookup never ran for the compaction turn")
	}
}
