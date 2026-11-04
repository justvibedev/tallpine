package acp

import (
	"fmt"
	"log/slog"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestIsACPRequestCancelled(t *testing.T) {
	cancelled := fmt.Errorf("ACP session/prompt: %w", &acpsdk.RequestError{Code: -32800, Message: "Request cancelled"})
	if !isACPRequestCancelled(cancelled) {
		t.Fatal("-32800 must classify as a stop")
	}
	if isACPRequestCancelled(&acpsdk.RequestError{Code: -32603, Message: "boom"}) {
		t.Fatal("real provider errors must stay errors")
	}
}

func settleWithError(t *testing.T, err error) ports.ChatEvent {
	t.Helper()
	conv := &conversation{
		sessionID:  "session-1",
		activeTurn: "turn-1",
		events:     make(chan ports.ChatEvent, 16),
		log:        slog.New(slog.DiscardHandler),
	}
	conv.finishPrompt("turn-1", acpsdk.PromptResponse{}, err)
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-conv.Events():
			if ev.Kind == ports.ChatEventTurnCompleted {
				return ev
			}
		case <-deadline:
			t.Fatal("no turn completion event")
		}
	}
}

func TestFinishPromptClassifiesCancelAsInterrupted(t *testing.T) {
	cancelled := settleWithError(t, &acpsdk.RequestError{Code: -32800, Message: "Request cancelled"})
	if cancelled.TurnState != domain.TurnStateInterrupted {
		t.Fatalf("-32800 state = %q, want interrupted", cancelled.TurnState)
	}
	failed := settleWithError(t, &acpsdk.RequestError{Code: -32603, Message: "boom"})
	if failed.TurnState != domain.TurnStateFailed {
		t.Fatalf("-32603 state = %q, want failed", failed.TurnState)
	}
}
