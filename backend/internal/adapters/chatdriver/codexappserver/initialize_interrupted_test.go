package codexappserver

import (
	"context"
	"errors"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// A handshake our deadline cut short (a loaded machine) is transient, not a
// protocol mismatch callers treat as permanent.
func TestInitializeDeadlineIsNotIncompatible(t *testing.T) {
	c, _ := newFakeAppServer(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := initializeConnection(ctx, c)
	if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ports.ErrChatDriverIncompatible) {
		t.Fatalf("initialize error = %v, want the deadline and not incompatible", err)
	}
}
