package acp

import (
	"context"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// Instructions ride after the command, collapsed to one line, which Claude
// Code reads as "/compact <instructions>".
func TestACPCompactWithInstructionsSendsThemAfterTheCommand(t *testing.T) {
	agent := &fakeAgent{}
	driver := newFakeDriver(agent, ports.ChatCapabilityCompaction)
	opened := startConversation(t, driver)
	_ = nextEvent(t, opened.Events())

	received := make(chan string, 1)
	agent.mu.Lock()
	agent.customPrompt = func(_ context.Context, params acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
		text := ""
		for _, block := range params.Prompt {
			if block.Text != nil {
				text += block.Text.Text
			}
		}
		received <- text
		return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
	}
	agent.mu.Unlock()

	if _, err := opened.(ports.ChatInstructedCompactor).CompactWithInstructions(context.Background(), "Keep the PR table.\n  Drop the rest."); err != nil {
		t.Fatalf("CompactWithInstructions: %v", err)
	}
	select {
	case got := <-received:
		if want := "/compact Keep the PR table. Drop the rest."; got != want {
			t.Fatalf("prompt = %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the compaction prompt")
	}
}
