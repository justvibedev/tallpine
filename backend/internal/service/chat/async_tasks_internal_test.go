package chat

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type closeOnlyConversation struct{ ports.ChatConversation }

func (closeOnlyConversation) Close() error { return nil }

type preservingConversation struct{ closeOnlyConversation }

func (preservingConversation) PreservesProviderOnClose() bool { return true }

func goneController(conv ports.ChatConversation, gone *[]domain.SessionID) *Controller {
	c := &Controller{sessionID: "s", conv: conv, stopped: make(chan struct{})}
	close(c.stopped)
	c.onAsyncTasksGone = func(id domain.SessionID) { *gone = append(*gone, id) }
	return c
}

// A harness that is replaced takes its background tasks with it, with no
// terminal frame, so the daemon must be told.
func TestTerminateAndCloseReportAsyncTasksGone(t *testing.T) {
	var gone []domain.SessionID
	if err := goneController(closeOnlyConversation{}, &gone).Terminate(context.Background()); err != nil || len(gone) != 1 {
		t.Fatalf("Terminate: err=%v gone=%v", err, gone)
	}
	gone = nil
	if err := goneController(closeOnlyConversation{}, &gone).Close(context.Background()); err != nil || len(gone) != 1 {
		t.Fatalf("Close of a non-persistent provider: err=%v gone=%v", err, gone)
	}
}

// A persistent host keeps the provider (and its tasks) alive for the replacement controller.
func TestCloseKeepsAsyncTasksWhenProviderIsPreserved(t *testing.T) {
	var gone []domain.SessionID
	if err := goneController(preservingConversation{}, &gone).Close(context.Background()); err != nil || len(gone) != 0 {
		t.Fatalf("Close of a preserved provider: err=%v gone=%v", err, gone)
	}
}
