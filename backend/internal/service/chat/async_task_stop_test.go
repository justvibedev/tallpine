package chat

import (
	"context"
	"errors"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type stoppableConv struct {
	ports.ChatConversation
	stopped bool
	seen    []string
}

func (c *stoppableConv) StopAsyncTask(_ context.Context, id string) (bool, error) {
	c.seen = append(c.seen, id)
	return c.stopped, nil
}

func TestServiceStopAsyncTaskReachesTheSessionsConversation(t *testing.T) {
	conv := &stoppableConv{stopped: true}
	s := &Service{controllers: map[domain.SessionID]*Controller{"s1": {conv: conv}, "s2": {conv: &plainConv{}}}}

	if stopped, err := s.StopAsyncTask(context.Background(), "s1", "bg-7"); err != nil || !stopped {
		t.Fatalf("stopped = %v, err = %v", stopped, err)
	}
	if len(conv.seen) != 1 || conv.seen[0] != "bg-7" {
		t.Fatalf("conversation saw %v", conv.seen)
	}
	if _, err := s.StopAsyncTask(context.Background(), "s2", "bg-7"); !errors.Is(err, ErrAsyncTaskStopUnsupported) {
		t.Fatalf("plain conversation: err = %v, want ErrAsyncTaskStopUnsupported", err)
	}
	if _, err := s.StopAsyncTask(context.Background(), "nope", "bg-7"); !errors.Is(err, ErrNoController) {
		t.Fatalf("no controller: err = %v, want ErrNoController", err)
	}
}
