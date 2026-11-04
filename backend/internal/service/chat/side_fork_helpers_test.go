package chat_test

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// sourceWithForkCurrent enables the current-fork capability on the production
// capability set, then opens the source chat.
func (h *sideWorkflow) sourceWithForkCurrent(t *testing.T) {
	t.Helper()
	caps := productionCaps()
	caps[ports.ChatCapabilityForkCurrent] = true
	h.conv.setCapabilities(caps)
	h.source(t)
}

// blockFork makes the driver's fork wait: started closes when it begins, and it
// completes successfully once release closes (or fails if its context ends).
func (h *sideWorkflow) blockFork() (started, release chan struct{}) {
	started, release = make(chan struct{}), make(chan struct{})
	h.driver.onFork = func(ctx context.Context) error {
		close(started)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return started, release
}
