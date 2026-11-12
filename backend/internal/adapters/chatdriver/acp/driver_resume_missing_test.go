package acp

import (
	"context"
	"errors"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// Only the agent's own not-found answer to session/load says the saved
// conversation is gone for good (B678); the orchestrator resume then starts a
// fresh one. Any other load failure may succeed on a retry and is not marked.
func TestACPResumeMarksOnlyANotFoundConversationMissing(t *testing.T) {
	for _, tt := range []struct {
		name        string
		loadErr     error
		wantMissing bool
	}{
		{"not found", &acpsdk.RequestError{Code: -32002, Message: "Resource not found"}, true},
		{"replay failure", errors.New("transcript replay failed"), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			driver := newFakeDriver(&fakeAgent{
				capabilities: &acpsdk.AgentCapabilities{LoadSession: true},
				failLoadFrom: 1,
				failLoadErr:  tt.loadErr,
			})
			_, err := driver.Resume(context.Background(), ports.ChatResumeConfig{
				ProviderConversationID: "provider-session-1", WorkspacePath: t.TempDir(),
			})
			if !errors.Is(err, ports.ErrChatResumeFailed) {
				t.Fatalf("Resume error = %v, want ErrChatResumeFailed", err)
			}
			if got := errors.Is(err, ports.ErrChatConversationMissing); got != tt.wantMissing {
				t.Fatalf("Resume error %v: missing = %v, want %v", err, got, tt.wantMissing)
			}
		})
	}
}
