package chat_test

import (
	"context"
	"sync"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type recordingActivity struct {
	mu      sync.Mutex
	signals []ports.ActivitySignal
	// forward, when set before any signal, also feeds each signal to a real consumer.
	forward func(context.Context, domain.SessionID, ports.ActivitySignal) error
}

func (r *recordingActivity) ApplyActivitySignal(
	ctx context.Context,
	id domain.SessionID,
	signal ports.ActivitySignal,
) error {
	r.mu.Lock()
	r.signals = append(r.signals, signal)
	r.mu.Unlock()
	if r.forward != nil {
		return r.forward(ctx, id, signal)
	}
	return nil
}

func (r *recordingActivity) snapshot() []ports.ActivitySignal {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]ports.ActivitySignal(nil), r.signals...)
}
