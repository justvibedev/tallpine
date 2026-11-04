package service

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/mobilebridge"
)

// tunnel reads the current connector without resolving one.
func (b *BridgeService) tunnel() TunnelController {
	b.tunnelMu.RLock()
	defer b.tunnelMu.RUnlock()
	return b.Tunnel
}

// ensureTunnel returns the connector, looking for a newly installed one first.
// Called where a connector is about to be needed, not on every read: probing
// the filesystem to answer a status poll would be wasteful.
func (b *BridgeService) ensureTunnel() TunnelController {
	b.awaitBootTunnel()
	if t := b.tunnel(); t != nil {
		return t
	}
	if b.ResolveTunnel == nil {
		return nil
	}
	// Resolution may inspect the filesystem. Keep it outside tunnelMu so Status
	// can continue reporting the currently known connector snapshot while that
	// work is slow. transitionMu ensures only one mutator resolves at a time.
	resolved := b.ResolveTunnel()
	if resolved == nil {
		return nil
	}
	b.tunnelMu.Lock()
	defer b.tunnelMu.Unlock()
	if b.Tunnel == nil {
		b.Tunnel = resolved
	}
	return b.Tunnel
}

// ResolveTunnelInBackground runs the boot-time connector lookup off the
// daemon's startup path. Until it finishes, status reads and ensureTunnel wait
// for its answer, so nothing reports remote access as unavailable, or looks up
// a second connector, in the moment before it lands.
func (b *BridgeService) ResolveTunnelInBackground(resolve func() TunnelController) {
	done := make(chan struct{})
	b.tunnelMu.Lock()
	b.tunnelResolving = done
	b.tunnelMu.Unlock()
	go func() {
		defer close(done)
		t := resolve()
		if t == nil {
			return
		}
		b.tunnelMu.Lock()
		defer b.tunnelMu.Unlock()
		if b.Tunnel == nil {
			b.Tunnel = t
		}
	}()
}

// awaitBootTunnel blocks until the boot-time lookup, if any, has finished. The
// lookup is bounded by the cloudflared version probe's timeout.
func (b *BridgeService) awaitBootTunnel() {
	b.tunnelMu.RLock()
	done := b.tunnelResolving
	b.tunnelMu.RUnlock()
	if done != nil {
		<-done
	}
}

func (b *BridgeService) tunnelEndpoint() *mobilebridge.TunnelEndpoint {
	t := b.tunnel()
	if t == nil {
		return nil
	}
	return t.Endpoint()
}

func (b *BridgeService) tunnelStatus() mobilebridge.TunnelStatus {
	b.awaitBootTunnel()
	t := b.tunnel()
	if t == nil {
		return mobilebridge.TunnelStatus{} // Supported stays false: nothing to run.
	}
	st := t.Status()
	st.Supported = true
	return st
}

func (b *BridgeService) queryTS() mobilebridge.TailscaleInfo {
	if b.QueryTS != nil {
		return b.QueryTS()
	}
	return mobilebridge.QueryTailscale(context.Background())
}

func (b *BridgeService) applyServe(port int) error {
	if b.ApplyServe != nil {
		return b.ApplyServe(port)
	}
	return mobilebridge.NewServe().Apply(context.Background(), port)
}

func (b *BridgeService) clearServe() error {
	if b.ClearServe != nil {
		return b.ClearServe()
	}
	return mobilebridge.NewServe().Clear(context.Background())
}

func (b *BridgeService) serveTarget() int {
	if b.ServeTarget != nil {
		return b.ServeTarget()
	}
	return mobilebridge.NewServe().Target(context.Background())
}

func (b *BridgeService) serveError() error {
	b.stateMu.RLock()
	defer b.stateMu.RUnlock()
	return b.serveErr
}

func (b *BridgeService) setServeError(err error) {
	b.stateMu.Lock()
	defer b.stateMu.Unlock()
	b.serveErr = err
}

// ShutdownTunnel stops the managed connector on the way out.
//
// The same reasoning as ShutdownServe: a cloudflared process outlives this
// daemon, so leaving it running would keep a public hostname resolving to a
// port that no longer has the authenticated LAN listener behind it. Reaping on
// the next boot recovers from a crash, but a clean quit should not depend on
// it — until the user happens to reopen the app the process keeps running and
// the hostname stays registered.
//
// Deliberately not touching persisted state: the bridge stays enabled, so boot
// restore brings the connector back on the next start. This ends the process,
// not the user's preference.
func (b *BridgeService) ShutdownTunnel() {
	b.transitionMu.Lock()
	defer b.transitionMu.Unlock()

	t := b.tunnel()
	if t == nil {
		return // Remote access is optional; nothing to stop without cloudflared.
	}
	t.Stop()
}

// ShutdownServe removes the tailnet proxy this bridge installed, for use on
// daemon shutdown. `tailscale serve --bg` state lives in tailscaled and
// outlives Tallpine, so without this the tailnet keeps routing to a local port that
// no longer has the authenticated LAN listener behind it — and any other
// process that later binds that port would be published to the tailnet in its
// place. The persisted SecurePairing preference is deliberately left set, so
// RestoreOnBoot re-applies the proxy against the next bound port.
func (b *BridgeService) ShutdownServe() {
	b.transitionMu.Lock()
	defer b.transitionMu.Unlock()

	st, _ := mobilebridge.Load(b.ConfigPath)
	if !st.Enabled || (!st.SecurePairing && !st.ServeCleanupPending) {
		return
	}
	_ = b.clearServe()
}

// closeTunnelPort drops the cloudflared loopback listener once the quick tunnel
// is off, so no extra port stays open.
func (b *BridgeService) closeTunnelPort() {
	if tp, ok := b.LAN.(interface{ CloseTunnelPort() }); ok {
		tp.CloseTunnelPort()
	}
}

// tunnelPort is where cloudflared connects: the listener's dedicated loopback
// port when it has one, so the tunnel's CF-Connecting-IP is trusted only there.
func (b *BridgeService) tunnelPort() int {
	if tp, ok := b.LAN.(interface{ OpenTunnelPort() int }); ok {
		if p := tp.OpenTunnelPort(); p > 0 {
			return p
		}
	}
	return b.LAN.BoundPort()
}
