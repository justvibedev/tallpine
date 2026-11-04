package service

import (
	"tallpine.eu/tallpine/backend/internal/mobilebridge"
)

// MobileStatusResponse is the body of the Connect Mobile status/enable/disable/
// regenerate endpoints. Password is populated only transiently, on enable and
// regenerate responses (empty otherwise) — it is never persisted in plaintext.
type MobileStatusResponse struct {
	// FeatureEnabled is the master mobile switch (default false). While false no
	// listener, pairing/push route, push relay or tunnel exists.
	FeatureEnabled bool `json:"featureEnabled"`
	// PublicBaseURL is the user-supplied reachable base URL (Tailscale name,
	// own reverse proxy) pairing codes advertise; empty when unset.
	PublicBaseURL string `json:"publicBaseUrl"`
	// PublicURLState says whether PublicBaseURL is actually served:
	// "not_configured", "ok", or "unreachable". An unreachable URL is not
	// advertised in Endpoints.
	PublicURLState string `json:"publicUrlState"`
	// QuickTunnel is the explicit Cloudflare quick-tunnel opt-in. Cloudflare
	// terminates TLS for that tunnel.
	QuickTunnel         bool `json:"quickTunnel"`
	Enabled             bool `json:"enabled"`
	BrowserRemoteAccess bool `json:"browserRemoteAccess"`
	// RequireDevicePairing ends legacy (8-char password) pairings when their
	// 14-day grace does, blocking older mobile apps. On by default.
	RequireDevicePairing bool `json:"requireDevicePairing"`
	// LegacyPasswordActive is true while an old 8-character connection password
	// still authenticates a phone. It is a static bearer: the desktop warns
	// until the phone is re-paired or RequireDevicePairing ends it.
	LegacyPasswordActive bool `json:"legacyPasswordActive"`
	// LegacyUntil is when the grace for legacy pairings ends (RFC 3339), empty when
	// no legacy password is configured.
	LegacyUntil string `json:"legacyUntil,omitempty"`
	// LoopbackOnly means direct LAN/Tailscale addresses are not listening.
	LoopbackOnly bool `json:"loopbackOnly"`
	// NoPublicTunnel is true unless the quick-tunnel opt-in is on.
	NoPublicTunnel bool `json:"noPublicTunnel"`
	// Endpoints is every way the phone can reach this daemon, in the client's
	// preference order. The phone races them; Host/TailscaleHost below are the
	// head of each kind, kept for the existing renderer.
	Endpoints []mobilebridge.Endpoint `json:"endpoints"`
	// HostID is this machine's stable identity, echoed into the pairing code.
	// The phone checks every endpoint it races against this value.
	HostID string `json:"hostId"`
	// Tunnel is the managed remote-access connector's state, so the desktop can
	// show progress during the tens of seconds before it is advertisable.
	Tunnel mobilebridge.TunnelStatus `json:"tunnel"`
	Host   string                    `json:"host"`
	// TailscaleHost is this machine's 100.64.0.0/10 Tailscale address, or "" when
	// Tailscale is not up. The renderer encodes it into the pairing QR when the
	// user selects the Tailscale tab, and shows a hint instead when it is empty.
	TailscaleHost string              `json:"tailscaleHost"`
	Port          int                 `json:"port"`
	Password      string              `json:"password"`
	Warning       string              `json:"warning"`
	SecurePairing SecurePairingStatus `json:"securePairing"`
	// TLSPin is the gateway certificate's public-key fingerprint, empty when the
	// gateway is not serving TLS.
	TLSPin    string          `json:"tlsPin"`
	KeepAwake KeepAwakeStatus `json:"keepAwake"`
}

// KeepAwakeStatus describes the macOS-only option that stops the machine from
// idle-sleeping while Connect Mobile is on, so a paired phone stays connected.
type KeepAwakeStatus struct {
	Supported    bool   `json:"supported"`  // this platform can hold the machine awake (macOS)
	Enabled      bool   `json:"enabled"`    // the user turned the option on
	Active       bool   `json:"active"`     // the sleep assertion is currently held
	HasBattery   bool   `json:"hasBattery"` // this machine has an internal battery
	LidClosed    bool   `json:"lidClosed,omitempty"`
	LidSupported bool   `json:"lidSupported,omitempty"`
	LidActive    bool   `json:"lidActive,omitempty"` // native control armed for this daemon's live lease
	LidReason    string `json:"lidReason,omitempty"`
}

// SecurePairingStatus describes the optional TLS-over-Tailscale pairing mode,
// in which `tailscale serve` fronts the bridge with a real certificate so iOS
// can pair by scanning (App Transport Security blocks cleartext to Tailscale's
// 100.64.0.0/10 range).
type SecurePairingStatus struct {
	Enabled   bool   `json:"enabled"`   // the user turned the mode on
	Available bool   `json:"available"` // CLI present, MagicDNS name known, certs enabled
	Active    bool   `json:"active"`    // proxy verified pointing at the live bridge port
	Host      string `json:"host"`      // MagicDNS name; "" when unknown
	Port      int    `json:"port"`      // 443 when active, else 0
	// Reason is a fixed enum the renderer maps to localized setup steps:
	// no_cli, no_magicdns, no_certs, serve_failed, port_mismatch, clear_failed.
	// Empty when Available. Never a raw error string — those are untranslated
	// and can leak paths.
	Reason string `json:"reason"`
}

// UnencryptedWarning is the constant notice stamped on every status response:
// the LAN leg is plain HTTP.
const UnencryptedWarning = "Traffic on this connection is not encrypted. Only use it on a network you trust."
