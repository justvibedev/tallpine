package daemon

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
)

const (
	// supervisorGrace is how long the daemon waits after its last frontend
	// disconnects before stopping. It tolerates a brief frontend restart.
	supervisorGrace = 5 * time.Second
	// hostLossMargin covers a host that dropped shortly before the frontend did.
	hostLossMargin = 5 * time.Second
)

// shutdownActivityFilter keeps a controller's stream ending during a daemon
// shutdown from being recorded as the agent exiting. When an app quit takes the
// provider hosts down with the daemon, their streams end while the daemon is
// stopping; persisting that as Exited (with no reason) reads at the next boot
// as an agent that stopped on its own, so startup never resumes it and the
// restart tracker never sees it as cut off (B622). Leaving the prior state in
// place lets the boot health check classify it as a lost host.
//
// A persistent host's socket dropping arrives as chat.controller.disconnected;
// other stream ends arrive as chat.controller.stopped.
//
// Daemon-owned teardown (IntentionalStop, such as parking at a deliberate quit)
// still passes through.
//
// A stream that ends before stopping turns true is still recorded, but noted in
// losses: a Force Quit reaches the daemon as supervisor EOF and b.ctx stays live
// through the grace period and the drain, so whether that exit was shutdown
// damage is only known once the shutdown happens (B627).
type shutdownActivityFilter struct {
	chatsvc.ActivityRecorder
	stopping func() bool
	losses   *hostLossLedger
}

func (f shutdownActivityFilter) ApplyActivitySignal(ctx context.Context, id domain.SessionID, s ports.ActivitySignal) error {
	if s.State == domain.ActivityExited && !s.IntentionalStop &&
		(s.Event == "chat.controller.stopped" || s.Event == "chat.controller.disconnected") {
		if f.stopping() {
			return nil
		}
		// Before forwarding: the note needs the state the session was in.
		f.losses.note(ctx, id)
	}
	return f.ActivityRecorder.ApplyActivitySignal(ctx, id, s)
}

// hostLossStore is the slice of the session store the ledger needs.
type hostLossStore interface {
	GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error)
	SetSessionAgentExitReason(context.Context, domain.SessionID, domain.AgentExitReason) error
}

type hostLoss struct {
	at      time.Time
	midTurn bool
}

// hostLossLedger remembers recent unintentional host losses, so a shutdown that
// follows within window can treat them as caused by it. A genuine crash that
// lands in the window is resumed too, which is the safe way to be wrong.
type hostLossLedger struct {
	store  hostLossStore
	window time.Duration
	now    func() time.Time

	mu     sync.Mutex
	losses map[domain.SessionID]hostLoss
}

func newHostLossLedger(store hostLossStore, window time.Duration) *hostLossLedger {
	return &hostLossLedger{store: store, window: window, now: time.Now}
}

// note records a loss. A session already exited is a repeat of the same loss,
// and rewriting it would forget that it was mid-turn.
func (l *hostLossLedger) note(ctx context.Context, id domain.SessionID) {
	if l == nil {
		return
	}
	rec, ok, err := l.store.GetSession(ctx, id)
	if err != nil || !ok || rec.Activity.State == domain.ActivityExited {
		return
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.losses == nil {
		l.losses = map[domain.SessionID]hostLoss{}
	}
	for known, loss := range l.losses {
		if now.Sub(loss.at) > l.window {
			delete(l.losses, known)
		}
	}
	l.losses[id] = hostLoss{at: now, midTurn: rec.Activity.State == domain.ActivityActive}
}

// settle marks every loss inside the window as a lost host, the classification
// that makes the next boot resume it, and hands the mid-turn ones to noteCutOff
// so the restart tracker resumes the turn or reports that it could not.
func (l *hostLossLedger) settle(ctx context.Context, noteCutOff func(domain.SessionID, time.Time)) {
	if l == nil {
		return
	}
	l.mu.Lock()
	losses := l.losses
	l.losses = nil
	l.mu.Unlock()
	now := l.now()
	for id, loss := range losses {
		if now.Sub(loss.at) > l.window {
			continue
		}
		rec, ok, err := l.store.GetSession(ctx, id)
		if err != nil || !ok || rec.IsTerminated || rec.Activity.State != domain.ActivityExited {
			continue
		}
		if err := l.store.SetSessionAgentExitReason(ctx, id, domain.AgentExitHostLost); err != nil {
			slog.Default().WarnContext(ctx, "could not mark a host lost before shutdown", "session", id, "err", err)
			continue
		}
		if loss.midTurn {
			noteCutOff(id, loss.at)
		}
	}
}
