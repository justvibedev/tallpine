package peeradmit

import (
	"context"
	"time"

	"tallpine.eu/tallpine/backend/internal/daemonsock"
)

const (
	defaultLaunchGrace = 5 * time.Second
	defaultRetryEvery  = 250 * time.Millisecond
)

// Mode is the effective mode: the build's mode unless the owner turned the
// check off. A nil Checker is off.
func (c *Checker) Mode() Mode {
	mode, _ := c.ModeAndReason()
	return mode
}

// ModeAndReason is Mode with why.
func (c *Checker) ModeAndReason() (Mode, string) {
	switch {
	case c == nil:
		return ModeOff, "no peer check"
	case c.opts.Mode == ModeOff:
		return ModeOff, c.opts.ModeReason
	case c.disabled.Load():
		return ModeOff, "turned off in the desktop app"
	}
	return c.opts.Mode, c.opts.ModeReason
}

// Active reports whether this build can check peers at all, whatever the
// owner's switch says.
func (c *Checker) Active() bool {
	return c != nil && c.opts.Mode != ModeOff
}

// SetDisabled applies the owner's off switch. It takes effect on the next
// request; a build whose mode is off stays off either way.
func (c *Checker) SetDisabled(off bool) {
	if c == nil {
		return
	}
	if c.disabled.Swap(off) != off && c.opts.Mode != ModeOff {
		mode, reason := c.ModeAndReason()
		c.opts.Log.Warn("peer check: mode changed", "mode", mode, "reason", reason)
	}
}

// Admit decides whether a request whose token is already verified may
// proceed. Off admits everything; report records off the request path and
// admits; enforce decides here and refuses a peer that is not admitted. A
// failure inside the decision refuses: an unidentified peer is never trusted.
func (c *Checker) Admit(ctx context.Context, obs Observation) (admitted bool) {
	return c.admit(ctx, obs, true)
}

// AdmitNoGrace is Admit without the launch grace: a scoped peer outside its
// roots is refused at once. For a request that can only come from an agent
// that already ran, such as a session-end hook, where roots that are gone mean
// the agent is gone, not that its launch is still being stored. The caller
// drops a refusal as expected, so it is neither logged nor listed as refused.
func (c *Checker) AdmitNoGrace(ctx context.Context, obs Observation) bool {
	return c.admit(ctx, obs, false)
}

func (c *Checker) admit(ctx context.Context, obs Observation, grace bool) (admitted bool) {
	switch c.Mode() {
	case ModeOff:
		return true
	case ModeReport:
		c.Observe(obs)
		return true
	}
	item := pending{obs: obs, peer: Peer{Transport: obs.Peer.Transport, Err: "peer check failed"}}
	decision := notAdmitted(ReasonPeerUnreadable)
	defer func() {
		if recovered := recover(); recovered != nil {
			c.opts.Log.Error("peer check: admit panicked", "panic", recovered)
			admitted = false
		}
		if grace || decision.Class != ClassNotAdmitted {
			c.record(item, decision, ModeEnforce)
		}
	}()
	item = c.capture(obs)
	decision = c.classify(ctx, &item)
	retries := 0
	if grace {
		retries = c.launchRetries()
	}
	for attempt := retries; attempt > 0 && launchMayLag(decision.Reason); attempt-- {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(c.opts.RetryEvery):
		}
		decision = Decide(item.peer, item.obs.Credential, c.freshRoots(ctx, item))
	}
	return decision.Class != ClassNotAdmitted
}

// DesktopCaller reports whether the request on ctx comes from the signed
// desktop app itself, which alone may change the off switch. A desktop-opened
// shell holds the install token too, but anything that can type into it could
// then turn the check off. A build that cannot check peers has nothing to
// guard, so every caller passes there. A ctx without a socket peer is not the
// desktop.
func (c *Checker) DesktopCaller(ctx context.Context) bool {
	if !c.Active() {
		return true
	}
	peer, ok := daemonsock.PeerFrom(ctx)
	if !ok {
		return false
	}
	item := c.capture(Observation{Peer: peer, Credential: Credential{Kind: CredentialInstall}})
	return c.classify(ctx, &item).Class == ClassDesktop
}

func (c *Checker) launchRetries() int {
	if c.opts.LaunchGrace < 0 {
		return 0
	}
	return int(c.opts.LaunchGrace / c.opts.RetryEvery)
}

// launchMayLag is a scoped-token refusal that a launch still being stored can
// explain: the session's runtime handle is written after its agent starts.
func launchMayLag(reason Reason) bool {
	return reason == ReasonScopedTokenOutside || reason == ReasonSessionRootsUnknown
}

func (c *Checker) freshRoots(ctx context.Context, item pending) Roots {
	ctx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()
	return c.resolve(ctx, item, true)
}
