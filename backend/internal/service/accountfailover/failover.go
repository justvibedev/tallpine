// Package accountfailover moves a session whose account hit a usage limit to
// another account of the same provider that has headroom, instead of waiting
// for the limit to reset.
package accountfailover

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

const (
	// DefaultCooldown is how long a session stays on the account it moved to
	// before it may move again, which also stops two limited accounts from
	// passing a session back and forth.
	DefaultCooldown = 30 * time.Minute
	// DefaultRetryAfter is how long a session waits after an attempt that found
	// no account or failed, so a scan every second does not read every account.
	DefaultRetryAfter = time.Minute
)

// Mover starts the durable move of a session's agent to another account.
type Mover interface {
	StartAccountTransition(ctx context.Context, id domain.SessionID, accountID string, policy domain.SessionInterfaceTransitionPolicy) (domain.SessionInterfaceTransition, error)
}

// Transitions reads a move's durable phase.
type Transitions interface {
	GetSessionInterfaceTransition(ctx context.Context, id string) (domain.SessionInterfaceTransition, bool, error)
}

// SessionAccounts names the account a session runs under.
type SessionAccounts interface {
	SessionAccountID(ctx context.Context, id domain.SessionID, harness domain.AgentHarness) (string, error)
}

// Config wires a Failover.
type Config struct {
	Pool        ports.AccountPool
	Sessions    SessionAccounts
	Mover       Mover
	Transitions Transitions
	// Enabled is read at each attempt; nil means always on.
	Enabled    func() bool
	Cooldown   time.Duration
	RetryAfter time.Duration
	Log        *slog.Logger
	Now        func() time.Time
}

// Failover picks and starts moves. It is safe for concurrent use.
type Failover struct {
	cfg Config

	mu    sync.Mutex
	until map[domain.SessionID]time.Time // no new attempt before this time
}

// New builds a Failover.
func New(cfg Config) *Failover {
	if cfg.Cooldown <= 0 {
		cfg.Cooldown = DefaultCooldown
	}
	if cfg.RetryAfter <= 0 {
		cfg.RetryAfter = DefaultRetryAfter
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Failover{cfg: cfg, until: map[domain.SessionID]time.Time{}}
}

// Failover starts a move of the session to the usable account with the most
// headroom. It returns the move's id, or "" when the session should wait for
// its limit to reset: no account has headroom, the session moved recently, or
// a read or the move itself failed. Failures are logged, never returned.
func (f *Failover) Failover(ctx context.Context, id domain.SessionID, harness domain.AgentHarness) string {
	if !f.Enabled() {
		return ""
	}
	provider, ok := domain.AgentAccountProviderFor(harness)
	if !ok {
		return ""
	}
	now := f.cfg.Now()
	f.mu.Lock()
	if now.Before(f.until[id]) {
		f.mu.Unlock()
		return ""
	}
	// Claim the attempt before reading, so concurrent scans of one session
	// start at most one move.
	f.until[id] = now.Add(f.cfg.RetryAfter)
	f.mu.Unlock()

	current, err := f.cfg.Sessions.SessionAccountID(ctx, id, harness)
	if err != nil || current == "" {
		f.cfg.Log.Warn("account failover: session account unreadable; waiting for reset", "session", id, "error", err)
		return ""
	}
	usable, err := f.cfg.Pool.Usable(ctx, provider)
	if err != nil {
		f.cfg.Log.Warn("account failover: accounts unreadable; waiting for reset", "session", id, "error", err)
		return ""
	}
	target, found := "", false
	best := 0.0
	for _, a := range usable {
		if a.ID == current {
			continue
		}
		limits, ok, err := f.cfg.Pool.Limits(ctx, a.ID)
		if err != nil {
			f.cfg.Log.Warn("account failover: limits unreadable; skipping account", "session", id, "account", a.ID, "error", err)
			continue
		}
		if !ok {
			continue // no reading: nothing shows the account has room
		}
		h, known := limits.Headroom()
		if !known || h <= 0 {
			continue
		}
		if !found || h > best {
			target, best, found = a.ID, h, true
		}
	}
	if !found {
		f.cfg.Log.Info("account failover: no account has headroom; waiting for reset", "session", id)
		return ""
	}
	transition, err := f.cfg.Mover.StartAccountTransition(ctx, id, target, domain.SessionInterfaceTransitionDrain)
	if err != nil {
		f.cfg.Log.Warn("account failover: move refused; waiting for reset", "session", id, "account", target, "error", err)
		return ""
	}
	f.mu.Lock()
	f.until[id] = f.cfg.Now().Add(f.cfg.Cooldown)
	f.mu.Unlock()
	f.cfg.Log.Info("account failover: moving session", "session", id, "from", current, "to", target)
	return transition.ID
}

// Enabled reports whether failover is switched on now.
func (f *Failover) Enabled() bool { return f.cfg.Enabled == nil || f.cfg.Enabled() }

// MovePhase is how a started move stands.
type MovePhase int

const (
	// MoveRunning means the move has not finished.
	MoveRunning MovePhase = iota
	// MoveDone means the session runs on the new account.
	MoveDone
	// MoveFailed means the move ended without changing the account.
	MoveFailed
)

// Phase reports a started move. A read failure counts as still running.
func (f *Failover) Phase(ctx context.Context, transitionID string) MovePhase {
	t, found, err := f.cfg.Transitions.GetSessionInterfaceTransition(ctx, transitionID)
	if err != nil {
		f.cfg.Log.Warn("account failover: move status unreadable", "transition", transitionID, "error", err)
		return MoveRunning
	}
	if !found {
		return MoveFailed
	}
	switch t.Phase {
	case domain.SessionInterfaceTransitionCompleted:
		return MoveDone
	case domain.SessionInterfaceTransitionFailed, domain.SessionInterfaceTransitionCancelled:
		return MoveFailed
	}
	return MoveRunning
}

// MoveFinished adapts Phase for the chat service.
func (f *Failover) MoveFinished(ctx context.Context, transitionID string) (finished, moved bool) {
	switch f.Phase(ctx, transitionID) {
	case MoveDone:
		return true, true
	case MoveFailed:
		return true, false
	}
	return false, false
}
