package service

import (
	"time"

	"tallpine.eu/tallpine/backend/internal/mobilebridge"
)

func (b *BridgeService) lanHosts() []string {
	if b.PickLANHosts != nil {
		return b.PickLANHosts()
	}
	return mobilebridge.LocalPrivateIPv4s()
}

func (b *BridgeService) tailscaleHosts() []string {
	if b.PickTailscaleHosts != nil {
		return b.PickTailscaleHosts()
	}
	return mobilebridge.LocalTailscaleIPv4s()
}

// first is the head of a candidate list, or "" when there is none. Callers use
// it for the legacy singular Host/TailscaleHost fields.
func first(hosts []string) string {
	if len(hosts) == 0 {
		return ""
	}
	return hosts[0]
}

// Status reports the current bridge state, host, and port. The plaintext
// password is included only while the bridge is enabled (loopback route only).
func (b *BridgeService) Status() MobileStatusResponse {
	st, _ := mobilebridge.Load(b.ConfigPath)
	enabled := st.Enabled && b.LAN.Running()
	lan, ts, eps, publicState := b.advertised(st)
	res := MobileStatusResponse{
		FeatureEnabled:       st.FeatureEnabled && !b.managedOff(),
		PublicBaseURL:        st.PublicBaseURL,
		QuickTunnel:          st.QuickTunnel,
		Enabled:              enabled,
		LoopbackOnly:         st.LoopbackOnly,
		NoPublicTunnel:       !st.QuickTunnel,
		Host:                 first(lan),
		TailscaleHost:        first(ts),
		Port:                 b.LAN.BoundPort(),
		Warning:              UnencryptedWarning,
		BrowserRemoteAccess:  st.BrowserRemoteAccess,
		RequireDevicePairing: st.RequireDevicePairing,
		LegacyPasswordActive: st.LegacyPasswordAccepted(time.Now()),
		Endpoints:            eps,
		PublicURLState:       publicState,
		Tunnel:               b.tunnelStatus(),
		HostID:               b.HostID,
		TLSPin:               b.TLSPin,
	}
	if st.LegacyPasswordHash != "" && !st.LegacyUntil.IsZero() {
		res.LegacyUntil = st.LegacyUntil.UTC().Format(time.RFC3339)
	}
	// Only surface the password while the bridge is actually enabled. This route
	// is reachable only on the loopback listener (the LAN listener 404s
	// /api/v1/mobile via lanControlBlock), so the plaintext never reaches a phone.
	if enabled {
		res.Password = b.displayPairingCode()
	}
	res.SecurePairing = b.securePairingStatus(st.SecurePairing, enabled)
	if st.ServeCleanupPending && res.SecurePairing.Reason == "" {
		res.SecurePairing.Reason = "clear_failed"
	}
	res.KeepAwake = b.keepAwakeStatus(st.KeepAwake, st.KeepAwakeLid)
	return res
}

// SetBrowserRemoteAccess persists the explicit browser-content opt-in. Turning
// it off revokes existing streams before the successful response is returned.
func (b *BridgeService) SetBrowserRemoteAccess(on bool) (MobileStatusResponse, error) {
	b.transitionMu.Lock()
	defer b.transitionMu.Unlock()
	st, err := mobilebridge.Load(b.ConfigPath)
	if err != nil {
		return MobileStatusResponse{}, err
	}
	st.BrowserRemoteAccess = on
	if err := mobilebridge.Save(b.ConfigPath, st); err != nil {
		return MobileStatusResponse{}, err
	}
	if !on && b.OnBrowserRemoteAccessDisabled != nil {
		b.OnBrowserRemoteAccessDisabled()
	}
	return b.Status(), nil
}

// SetRequireDevicePairing persists the setting (on by default) that ends legacy pairings when
// their grace window does, and applies it to the running listener.
func (b *BridgeService) SetRequireDevicePairing(on bool) (MobileStatusResponse, error) {
	b.transitionMu.Lock()
	defer b.transitionMu.Unlock()
	st, err := mobilebridge.Load(b.ConfigPath)
	if err != nil {
		return MobileStatusResponse{}, err
	}
	st.RequireDevicePairing = on
	// Turning the setting off lets a window that merely ended keep working, but
	// must not resurrect a password the user retired with Regenerate.
	dropExpired := !on && st.LegacyPasswordHash != "" && st.LegacyRevoked
	if dropExpired {
		st.LegacyPasswordHash, st.LegacyUntil, st.LegacyRevoked = "", time.Time{}, false
	}
	if err := mobilebridge.Save(b.ConfigPath, st); err != nil {
		return MobileStatusResponse{}, err
	}
	if lc, ok := b.LAN.(lanCredentials); ok {
		if dropExpired {
			lc.SetLegacyPassword("", time.Time{})
		}
		lc.SetRequireDevicePairing(on)
	}
	return b.Status(), nil
}

// AdvertisedEndpoints reports how this daemon can currently be reached, for
// the phone's refresh route. Same list Status carries, so the two cannot drift.
func (b *BridgeService) AdvertisedEndpoints() []mobilebridge.Endpoint {
	st, _ := mobilebridge.Load(b.ConfigPath)
	_, _, eps, _ := b.advertised(st)
	return eps
}

// publicEndpoint is the user-supplied base URL as an advertised endpoint, or
// nil when mobile is off, none is set, or the stored value no longer parses.
func publicEndpoint(st mobilebridge.State) *mobilebridge.Endpoint {
	if !st.FeatureEnabled || st.PublicBaseURL == "" {
		return nil
	}
	ep, err := mobilebridge.ParsePublicBaseURL(st.PublicBaseURL)
	if err != nil {
		return nil
	}
	return &ep
}

// securePairingStatus assembles the SecurePairing block of Status from the
// persisted mode flag and current bridge/proxy state.
func (b *BridgeService) securePairingStatus(on, bridgeUp bool) SecurePairingStatus {
	serveErr := b.serveError()
	sp := SecurePairingStatus{Enabled: on}
	if !on {
		// The mode is off, but a failed Clear may have left the proxy live —
		// report that without touching the network (no queryTS/serveTarget
		// calls when the mode is off).
		if serveErr != nil {
			sp.Reason = "clear_failed"
		}
		return sp
	}
	info := b.queryTS()
	switch {
	case info.Name == "":
		sp.Reason = "no_cli"
		return sp
	case !info.CertsEnabled:
		sp.Host = info.Name
		sp.Reason = "no_certs"
		return sp
	}
	sp.Available, sp.Host = true, info.Name
	if !bridgeUp {
		return sp
	}
	if serveErr != nil {
		sp.Reason = "serve_failed"
		return sp
	}
	if b.serveTarget() != b.LAN.BoundPort() {
		sp.Reason = "port_mismatch"
		return sp
	}
	sp.Active, sp.Port = true, 443
	return sp
}
