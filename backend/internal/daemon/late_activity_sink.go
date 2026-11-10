package daemon

import (
	"context"
	"sync/atomic"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type activityApplier interface {
	ApplyActivitySignal(ctx context.Context, id domain.SessionID, signal ports.ActivitySignal) error
}

// lateActivitySink is where the screen poller sends its activity signals. It
// starts on the lifecycle manager and is rebound to the turn notifier once
// that exists (it is built after startLifecycle), so a prompt only the screen
// shows, such as Kiro's approval bar, also reaches the orchestrator notice.
type lateActivitySink struct {
	target atomic.Pointer[activityApplier]
}

func newLateActivitySink(initial activityApplier) *lateActivitySink {
	s := &lateActivitySink{}
	s.Bind(initial)
	return s
}

// Bind replaces the applier that receives every later signal.
func (s *lateActivitySink) Bind(a activityApplier) { s.target.Store(&a) }

func (s *lateActivitySink) ApplyActivitySignal(ctx context.Context, id domain.SessionID, sig ports.ActivitySignal) error {
	return (*s.target.Load()).ApplyActivitySignal(ctx, id, sig)
}

// screenPollTick is how often the screen poller reads agents whose permission
// dialogs fire no hook (Kiro), so such a prompt reaches the orchestrator and
// the Inbox within seconds instead of the 30s default.
const screenPollTick = 5 * time.Second
