package daemon

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type countingApplier struct{ got []ports.ActivitySignal }

func (c *countingApplier) ApplyActivitySignal(_ context.Context, _ domain.SessionID, s ports.ActivitySignal) error {
	c.got = append(c.got, s)
	return nil
}

// Signals the screen poller sends before the turn notifier exists reach the
// lifecycle manager; once it is bound they reach the notifier instead.
func TestLateActivitySinkRoutesPollerSignalsToTheBoundNotifier(t *testing.T) {
	lcm, notifier := &countingApplier{}, &countingApplier{}
	sink := newLateActivitySink(lcm)
	sig := ports.ActivitySignal{Valid: true, State: domain.ActivityWaitingInput, Event: "terminal-waiting-input"}
	_ = sink.ApplyActivitySignal(context.Background(), "w1", sig)
	sink.Bind(notifier)
	_ = sink.ApplyActivitySignal(context.Background(), "w1", sig)
	if len(lcm.got) != 1 || len(notifier.got) != 1 {
		t.Fatalf("lcm=%d notifier=%d, want 1 each", len(lcm.got), len(notifier.got))
	}
}
