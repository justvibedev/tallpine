package acp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// A handshake cut short is transient, not a protocol mismatch: the restart
// that left agent-orchestrator-808 stuck logged "chat driver incompatible:
// ACP initialize: Request cancelled" on a machine at 500% CPU.
func TestACPInitializeInterruptionIsNotIncompatible(t *testing.T) {
	t.Run("agent cancelled the request", func(t *testing.T) {
		agent := &fakeAgent{initErr: &acpsdk.RequestError{Code: -32800, Message: "Request cancelled"}}
		driver := newDriverWith(fakeConfig(ports.ChatCapabilityResume), agent)
		_, err := driver.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
		if err == nil || errors.Is(err, ports.ErrChatDriverIncompatible) || !strings.Contains(err.Error(), "Request cancelled") {
			t.Fatalf("Start error = %v, want a transient cancelled handshake", err)
		}
	})
	t.Run("our deadline ended the handshake", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
		defer cancel()
		<-ctx.Done()
		err := initializeInterrupted(ctx, errors.New("read: connection reset"))
		if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ports.ErrChatDriverIncompatible) {
			t.Fatalf("interrupted = %v, want the deadline and not incompatible", err)
		}
		if err := initializeInterrupted(context.Background(), errors.New("bad frame")); err != nil {
			t.Fatalf("live handshake failure classified as interrupted: %v", err)
		}
	})
	t.Run("a real handshake failure stays incompatible", func(t *testing.T) {
		agent := &fakeAgent{initErr: &acpsdk.RequestError{Code: -32602, Message: "unsupported protocol"}}
		driver := newDriverWith(fakeConfig(ports.ChatCapabilityResume), agent)
		_, err := driver.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
		if !errors.Is(err, ports.ErrChatDriverIncompatible) {
			t.Fatalf("Start error = %v, want incompatible", err)
		}
	})
}
