package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/mobilebridge"
)

// SetSecurePairing turns TLS-over-Tailscale pairing on or off, persisting the
// choice. Turning it on applies the proxy immediately when the bridge is
// already running; turning it off tears down only a proxy this bridge owns.
func (b *BridgeService) SetSecurePairing(on bool) (MobileStatusResponse, error) {
	b.transitionMu.Lock()
	defer b.transitionMu.Unlock()

	st, _ := mobilebridge.Load(b.ConfigPath)
	if on && st.LoopbackOnly {
		return MobileStatusResponse{}, errors.New("tailscale secure pairing is unavailable in tunnel-only mode")
	}
	ownedServe := st.SecurePairing || st.ServeCleanupPending
	st.SecurePairing = on
	if on {
		st.ServeCleanupPending = false
	} else if ownedServe {
		st.ServeCleanupPending = true
	}
	if err := mobilebridge.Save(b.ConfigPath, st); err != nil {
		return MobileStatusResponse{}, err
	}
	b.setServeError(nil)
	if on {
		if b.LAN.Running() {
			b.setServeError(b.applyServe(b.LAN.BoundPort()))
		}
	} else if ownedServe {
		// Persist pending cleanup before touching the node-global Serve route:
		// a crash or failed clear must not make tunnel-only setup trust this port.
		err := b.clearServe()
		b.setServeError(err)
		if err == nil && st.ServeCleanupPending {
			st.ServeCleanupPending = false
			if err := mobilebridge.Save(b.ConfigPath, st); err != nil {
				return MobileStatusResponse{}, err
			}
		}
	}
	return b.Status(), nil
}

// enableWithPasswordLocked performs an enable or password rotation while the
// caller owns transitionMu.
func (b *BridgeService) enableWithPasswordLocked(pw string, quickTunnel, loopbackOnly bool) (MobileStatusResponse, error) {
	if b.managedOff() {
		return MobileStatusResponse{}, ErrManagedOff
	}
	// Snapshot state so we can roll back the in-memory side effects (armed hash,
	// running listener) if we fail before durable state is written. Otherwise a
	// failed enable would leave a LAN listener open on 0.0.0.0 with the new
	// password while persisted state/UI still say the bridge is off.
	prevHash := b.LAN.PasswordHash()
	prevCode := b.shownPairingCode()
	wasRunning := b.LAN.Running()
	prevSt, _ := mobilebridge.Load(b.ConfigPath)

	// A fresh listener must be armed before binding. For an already-running
	// listener, wait until persistence succeeds before rotating the hash: a
	// failed save must not disconnect currently authenticated clients.
	if !wasRunning {
		b.armPairingCode(pw)
	}
	start := b.LAN.Start
	if loopbackOnly {
		start = b.LAN.StartLoopback
	}
	// Prefer the port phones were paired against; the default only seeds a
	// first run. Otherwise a disable/enable cycle would drift to whatever is free.
	wantPort := b.DefaultPort
	if prevSt.LastPort > 0 {
		wantPort = prevSt.LastPort
	}
	port, err := start(wantPort)
	if err != nil {
		if !wasRunning {
			b.restorePairing(prevCode, prevHash) // Start failed: undo the hash swap.
		}
		return MobileStatusResponse{}, err
	}
	// Preserve the persisted SecurePairing and KeepAwake flags while selecting
	// whether this enable is allowed to start the opt-in quick tunnel.
	nextSt := mobilebridge.State{FeatureEnabled: prevSt.FeatureEnabled, PublicBaseURL: prevSt.PublicBaseURL, Enabled: true, LegacyPasswordHash: prevSt.LegacyPasswordHash, LegacyUntil: prevSt.LegacyUntil, LegacyRevoked: prevSt.LegacyRevoked, RequireDevicePairing: prevSt.RequireDevicePairing, LastPort: port, SecurePairing: prevSt.SecurePairing, ServeCleanupPending: prevSt.ServeCleanupPending, KeepAwake: prevSt.KeepAwake, KeepAwakeLid: prevSt.KeepAwakeLid, BrowserRemoteAccess: prevSt.BrowserRemoteAccess, QuickTunnel: quickTunnel, LoopbackOnly: loopbackOnly}
	if err := mobilebridge.Save(b.ConfigPath, nextSt); err != nil {
		// Persist failed after the listener came up. A running listener still
		// serves the old hash; a fresh enable must tear the listener back down.
		if !wasRunning {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = b.LAN.Stop(ctx)
			b.restorePairing(prevCode, prevHash)
		}
		return MobileStatusResponse{}, err
	}
	if wasRunning {
		b.armPairingCode(pw)
	}
	b.armLANCredentials(nextSt)
	// Re-point the proxy at the port Start actually bound. This runs on every
	// listener start driven through this method — enable and password rotation
	// both funnel through here — which is what keeps the proxy off a dead port
	// after an ephemeral fallback. A daemon restart does NOT go through this
	// method (it has no password to rotate); see RestoreOnBoot, which mirrors
	// this same post-Start apply. A failure is recorded, never fatal: the
	// bridge stays up in plaintext mode and Status reports serve_failed.
	b.startConnectorsLocked(port, nextSt)
	return b.Status(), nil
}

// startConnectorsLocked is the shared post-Start step for fresh enables,
// password rotations, and boot restoration. The caller owns transitionMu.
func (b *BridgeService) startConnectorsLocked(port int, st mobilebridge.State) {
	b.setServeError(nil)
	if st.SecurePairing && !st.LoopbackOnly {
		b.setServeError(b.applyServe(port))
	}
	// Best-effort like the proxy: a failed caffeinate leaves the bridge up and
	// Status reports the assertion as inactive.
	_ = b.applyKeepAwakeLocked(st.KeepAwake && st.Enabled, st.KeepAwakeLid)
	// Point the connector at the port Start actually bound, not DefaultPort:
	// Start falls back to an ephemeral port when the default is taken, and a
	// connector aimed at the wrong port tunnels nothing. Start is idempotent
	// and returns immediately — the connector needs tens of seconds to become
	// advertisable, and Status reports that progress meanwhile.
	// Resolve here rather than only at boot: this is the moment a connector
	// installed since then should start being used.
	// Only the explicit quick-tunnel opt-in ever starts a connector.
	if st.QuickTunnel {
		if t := b.ensureTunnel(); t != nil {
			t.Start(b.tunnelPort())
		}
	}
}

// RestoreOnBoot re-arms the LAN listener from persisted state across a daemon
// restart, reusing the existing password (no rotation — an already-paired
// phone keeps working) and re-applying the secure-pairing proxy against the
// port Start actually bound, never the persisted LastPort. That distinction is
// the entire point of this method: Start falls back to an ephemeral port when
// LastPort is taken (e.g. by another Tallpine instance), and a `tailscale serve`
// config pinned to a stale port would proxy the tailnet at this machine's
// hostname to whatever now holds that port. A failure to apply is recorded in
// serveErr, never returned — the caller (restoreMobileOnBoot) treats this as
// best-effort and must never block daemon boot on it.
func (b *BridgeService) RestoreOnBoot(state mobilebridge.State) error {
	b.transitionMu.Lock()
	defer b.transitionMu.Unlock()
	if b.managedOff() {
		return nil
	}

	prevHash := b.LAN.PasswordHash()
	// The pairing code is never persisted: mint a fresh one for this run.
	// Paired phones authenticate with device tokens (or the legacy bearer),
	// so none of them depends on the old code.
	if err := b.mintPairingCode(); err != nil {
		return err
	}
	b.armLANCredentials(state)
	start := b.LAN.Start
	if state.LoopbackOnly {
		start = b.LAN.StartLoopback
	}
	port, err := start(state.LastPort)
	if err != nil {
		b.LAN.SetPasswordHash(prevHash)
		return err
	}
	b.startConnectorsLocked(port, state)
	return nil
}

// Enable generates a fresh password and starts the LAN listener. If it is
// already running, the password changes only after the new state is saved.
func (b *BridgeService) Enable() (MobileStatusResponse, error) {
	b.transitionMu.Lock()
	defer b.transitionMu.Unlock()
	st, _ := mobilebridge.Load(b.ConfigPath)
	if st.LoopbackOnly && b.LAN.Running() {
		return MobileStatusResponse{}, errors.New("disable the loopback-only listener before enabling LAN access")
	}

	pw, err := mobilebridge.GeneratePassword()
	if err != nil {
		return MobileStatusResponse{}, err
	}
	return b.enableWithPasswordLocked(pw, st.QuickTunnel, false)
}

// EnableLANOnly opts into the existing authenticated listener without
// publishing it through the managed public tunnel. An already-running bridge
// keeps its password so paired clients are not dropped.
func (b *BridgeService) EnableLANOnly() (MobileStatusResponse, error) {
	b.transitionMu.Lock()
	defer b.transitionMu.Unlock()

	st, err := mobilebridge.Load(b.ConfigPath)
	if err != nil {
		return MobileStatusResponse{}, err
	}
	if st.LoopbackOnly && b.LAN.Running() {
		return MobileStatusResponse{}, errors.New("disable the loopback-only listener before enabling LAN access")
	}
	// A connector may still be running even if the listener went down. Stop it
	// before any attempt to re-arm the private listener.
	if t := b.tunnel(); t != nil {
		t.Stop()
	}
	if st.Enabled && b.LAN.Running() {
		if st.QuickTunnel {
			st.QuickTunnel = false
			if err := mobilebridge.Save(b.ConfigPath, st); err != nil {
				return MobileStatusResponse{}, err
			}
			b.closeTunnelPort()
		}
		return b.Status(), nil
	}
	pw, err := mobilebridge.GeneratePassword()
	if err != nil {
		return MobileStatusResponse{}, err
	}
	return b.enableWithPasswordLocked(pw, false, false)
}

// EnableTunnelOnly is for a headless host reached solely through a local
// connector. Repeating it preserves the password; an existing LAN listener
// must be disabled explicitly rather than silently claimed as private.
func (b *BridgeService) EnableTunnelOnly() (MobileStatusResponse, error) {
	b.transitionMu.Lock()
	defer b.transitionMu.Unlock()
	st, err := mobilebridge.Load(b.ConfigPath)
	if err != nil {
		return MobileStatusResponse{}, err
	}
	if b.LAN.Running() {
		if !st.LoopbackOnly {
			return MobileStatusResponse{}, errors.New("listener already exposed on LAN; run `tallpine remote-host disable` before tunnel-only setup")
		}
		if _, err := b.LAN.StartLoopback(b.LAN.BoundPort()); err != nil {
			return MobileStatusResponse{}, err
		}
		if !st.QuickTunnel {
			st.QuickTunnel = true
			if err := mobilebridge.Save(b.ConfigPath, st); err != nil {
				return MobileStatusResponse{}, err
			}
		}
		if t := b.ensureTunnel(); t != nil {
			t.Start(b.tunnelPort())
		}
		return b.Status(), nil
	}
	if st.ServeCleanupPending || (st.Enabled && st.SecurePairing) {
		return MobileStatusResponse{}, errors.New("tailscale Serve may still be active; inspect it and run `tallpine remote-host disable` before tunnel-only setup")
	}
	if st.SecurePairing {
		// Disable already cleared Tallpine's route. Forget only the saved preference;
		// another user may now own the node-global Serve address.
		st.SecurePairing = false
		if err := mobilebridge.Save(b.ConfigPath, st); err != nil {
			return MobileStatusResponse{}, err
		}
	}
	pw, err := mobilebridge.GeneratePassword()
	if err != nil {
		return MobileStatusResponse{}, err
	}
	return b.enableWithPasswordLocked(pw, true, true)
}

// StartRemoteAccess looks for a connector again and starts it against the port
// already bound, without touching the connection password.
//
// Exists because the obvious alternative is wrong: re-enabling would make the
// daemon re-resolve, but Enable mints a fresh password, so installing
// cloudflared would silently invalidate the phone that was already paired —
// the user sets up remote access and loses the connection they were setting it
// up for.
//
// A no-op while the bridge is disabled: there is no bound port to tunnel to,
// and enabling is the user's decision to make, not a side effect of installing
// a binary.
func (b *BridgeService) StartRemoteAccess() (MobileStatusResponse, error) {
	b.transitionMu.Lock()
	defer b.transitionMu.Unlock()

	st, err := mobilebridge.Load(b.ConfigPath)
	if err != nil {
		return MobileStatusResponse{}, err
	}
	if !st.Enabled || !b.LAN.Running() {
		return b.Status(), nil
	}
	if !st.QuickTunnel {
		st.QuickTunnel = true
		if err := mobilebridge.Save(b.ConfigPath, st); err != nil {
			return MobileStatusResponse{}, err
		}
	}
	if t := b.ensureTunnel(); t != nil {
		t.Start(b.tunnelPort())
	}
	return b.Status(), nil
}

// Regenerate mints a new pairing code and ends the legacy-password grace
// window. Phones paired with a device token stay connected.
func (b *BridgeService) Regenerate() (MobileStatusResponse, error) {
	b.transitionMu.Lock()
	defer b.transitionMu.Unlock()

	pw, err := mobilebridge.GeneratePassword()
	if err != nil {
		return MobileStatusResponse{}, err
	}
	st, err := mobilebridge.Load(b.ConfigPath)
	if err != nil {
		return MobileStatusResponse{}, err
	}
	// With per-device pairing required, a new code also ends the legacy-password
	// grace window. The hash is kept with an expired window (not wiped) so the
	// old phone is told LEGACY_PAIRING_EXPIRED rather than BAD_PASSWORD; otherwise the legacy pairing (an older app that cannot
	// re-pair) survives it. Paired devices keep their tokens and are revoked per
	// device in the devices list.
	if st.RequireDevicePairing && st.LegacyPasswordHash != "" && !st.LegacyRevoked {
		if st.LegacyUntil.After(time.Now()) {
			st.LegacyUntil = time.Now()
		}
		st.LegacyRevoked = true
		if err := mobilebridge.Save(b.ConfigPath, st); err != nil {
			return MobileStatusResponse{}, err
		}
	}
	return b.enableWithPasswordLocked(pw, st.QuickTunnel, st.LoopbackOnly)
}

// Disable stops the LAN listener and persists the disabled state.
func (b *BridgeService) Disable() error {
	b.transitionMu.Lock()
	defer b.transitionMu.Unlock()
	return b.disableLocked(false)
}

// disableLocked stops everything the bridge started. featureOff additionally
// clears the feature flag in the same persisted write. The caller owns
// transitionMu.
func (b *BridgeService) disableLocked(featureOff bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Stop the connector first. Leaving it up after the user turned Connect
	// Mobile off would keep the machine reachable from the internet while the
	// UI says it is not.
	if t := b.tunnel(); t != nil {
		t.Stop()
	}
	// No phone to stay reachable for once the bridge is off. The preference
	// itself is kept, so turning the bridge back on resumes it.
	if b.KeepAwake != nil {
		b.KeepAwake.Stop()
	}
	stopErr := b.LAN.Stop(ctx)
	if stopErr != nil && b.LAN.Running() {
		return stopErr
	}
	// Forced cleanup can finish after the graceful-shutdown deadline. Persist
	// the disabled preference once stopped, while still returning that error.
	st, _ := mobilebridge.Load(b.ConfigPath)
	// Only touch the tailnet proxy when this bridge actually installed one.
	// `tailscale serve --https=443 off` is node-global state: clearing it
	// unconditionally would destroy a serve route the user configured for
	// themselves, or one owned by another Tallpine instance, for someone who never
	// enabled secure pairing at all.
	if st.SecurePairing || st.ServeCleanupPending {
		st.ServeCleanupPending = b.clearServe() != nil
	}
	st.Enabled = false
	if featureOff {
		st.FeatureEnabled = false
	}
	return errors.Join(stopErr, mobilebridge.Save(b.ConfigPath, st))
}

// SetFeatureEnabled flips the master mobile switch. Turning it on only unlocks
// the mobile surface; the listener still starts on an explicit Enable. Turning
// it off tears down the listener, quick tunnel, serve proxy and keep-awake hold
// and revokes live browser streams before returning.
func (b *BridgeService) SetFeatureEnabled(on bool) (MobileStatusResponse, error) {
	b.transitionMu.Lock()
	defer b.transitionMu.Unlock()
	if on {
		if b.managedOff() {
			return MobileStatusResponse{}, ErrManagedOff
		}
		st, err := mobilebridge.Load(b.ConfigPath)
		if err != nil {
			return MobileStatusResponse{}, err
		}
		st.FeatureEnabled = true
		if err := mobilebridge.Save(b.ConfigPath, st); err != nil {
			return MobileStatusResponse{}, err
		}
		return b.Status(), nil
	}
	if err := b.disableLocked(true); err != nil {
		return MobileStatusResponse{}, err
	}
	if b.OnBrowserRemoteAccessDisabled != nil {
		b.OnBrowserRemoteAccessDisabled()
	}
	return b.Status(), nil
}

// SetPublicBaseURL stores the reachable base URL pairing codes advertise, or
// clears it when raw is empty. The URL is validated, never fetched.
func (b *BridgeService) SetPublicBaseURL(raw string) (MobileStatusResponse, error) {
	raw = strings.TrimSpace(raw)
	canonical := ""
	if raw != "" {
		ep, err := mobilebridge.ParsePublicBaseURL(raw)
		if err != nil {
			return MobileStatusResponse{}, err
		}
		canonical = ep.PublicBaseURLString()
	}
	b.transitionMu.Lock()
	defer b.transitionMu.Unlock()
	st, err := mobilebridge.Load(b.ConfigPath)
	if err != nil {
		return MobileStatusResponse{}, err
	}
	st.PublicBaseURL = canonical
	if err := mobilebridge.Save(b.ConfigPath, st); err != nil {
		return MobileStatusResponse{}, err
	}
	return b.Status(), nil
}

// SetQuickTunnel is the explicit Cloudflare quick-tunnel opt-in. Off stops a
// running connector immediately.
func (b *BridgeService) SetQuickTunnel(on bool) (MobileStatusResponse, error) {
	b.transitionMu.Lock()
	defer b.transitionMu.Unlock()
	st, err := mobilebridge.Load(b.ConfigPath)
	if err != nil {
		return MobileStatusResponse{}, err
	}
	st.QuickTunnel = on
	if err := mobilebridge.Save(b.ConfigPath, st); err != nil {
		return MobileStatusResponse{}, err
	}
	if !on {
		if t := b.tunnel(); t != nil {
			t.Stop()
		}
		b.closeTunnelPort()
	} else if t := b.ensureTunnel(); t != nil && st.Enabled && b.LAN.Running() {
		t.Start(b.tunnelPort())
	}
	return b.Status(), nil
}

// ErrManagedOff is returned by every enable path while the managed policy
// switches mobile off.
var ErrManagedOff = errors.New("mobile is turned off by your organization")

func (b *BridgeService) managedOff() bool { return b.ManagedOffNow() }

// ManagedOffNow reports whether the managed policy currently switches mobile off.
func (b *BridgeService) ManagedOffNow() bool { return b.ManagedOff != nil && b.ManagedOff() }

// EnforceManagedOff tears the bridge down when the managed policy switches
// mobile off, and persists the feature flag as off. It is a no-op otherwise.
func (b *BridgeService) EnforceManagedOff() error {
	if !b.managedOff() {
		return nil
	}
	b.transitionMu.Lock()
	defer b.transitionMu.Unlock()
	if err := b.disableLocked(true); err != nil {
		return err
	}
	if b.OnBrowserRemoteAccessDisabled != nil {
		b.OnBrowserRemoteAccessDisabled()
	}
	return nil
}
