package chat_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// lockedBuffer is a goroutine-safe log sink.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// A slow failed send on either relay path is logged with its real error.
func TestSlowFailedRelayLogsItsError(t *testing.T) {
	t.Parallel()
	for name, relay := range map[string]func(*harness) error{
		"receipt": func(h *harness) error {
			_, err := h.svc.RelaySessionChatTurnReceipt(context.Background(), testSession, "hi", "", ports.MessageDeliveryOptions{})
			return err
		},
		"turn": func(h *harness) error {
			_, err := h.svc.RelaySessionChatTurn(context.Background(), testSession, "hi", "", ports.MessageDeliveryOptions{})
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.conv.mu.Lock()
			h.conv.sendErr = errors.New("provider stalled")
			h.conv.beforeSend = func() { h.advance(7 * time.Second) }
			h.conv.mu.Unlock()
			if err := relay(h); err == nil {
				t.Fatal("relay succeeded, want the provider error")
			}
			out := h.logs.String()
			if !strings.Contains(out, "slow chat send") || !strings.Contains(out, "provider stalled") || !strings.Contains(out, "dispatch=7s") {
				t.Fatalf("slow relay log = %q", out)
			}
		})
	}
}
