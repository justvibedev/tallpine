package service

import (
	"context"
	"sync"

	"tallpine.eu/tallpine/backend/internal/mobilebridge"
)

// LANController is the runtime hook set the concrete bridge needs. httpd's
// LANManager + authState satisfy it (adapter wired in daemon.go).
type LANController interface {
	Start(port int) (int, error)
	StartLoopback(port int) (int, error)
	Stop(ctx context.Context) error
	Running() bool
	BoundPort() int
	SetPasswordHash(hash string)
	PasswordHash() string
}

// TunnelController is the managed remote-access connector, as the bridge sees
// it. Start is idempotent and returns immediately; the connector takes tens of
// seconds to become advertisable, so callers must not block on it.
type TunnelController interface {
	Start(localPort int)
	Stop()
	// Endpoint is the advertisable tunnel, or nil while it is starting,
	// settling, or down.
	Endpoint() *mobilebridge.TunnelEndpoint
	Status() mobilebridge.TunnelStatus
}

// KeepAwakeController holds the machine awake while the bridge is on. Start and
// Stop are idempotent.
type KeepAwakeController interface {
	Start() error
	Stop()
	Active() bool
	HasBattery() bool
	ConfigureLid(on, allowSetup bool) error
	LidStatus() (supported, active bool, reason string)
}

// BridgeService is the production mobileBridge. It persists state and drives
// the LAN listener. Password plaintext exists only transiently in the response.
type BridgeService struct {
	LAN         LANController
	ConfigPath  string
	DefaultPort int
	// PickLANHosts and PickTailscaleHosts resolve every advertised address.
	// Both are nil in production (daemon.go) and fall back to the real
	// candidate scans; tests inject stubs so status output does not depend on
	// the host machine's real network interfaces.
	//
	// Lists, not single addresses: the phone races every endpoint, so a machine
	// on both Wi-Fi and Ethernet must advertise both. Host and TailscaleHost are
	// derived from the head of each list so the singular fields and the list can
	// never disagree.
	PickLANHosts       func() []string
	PickTailscaleHosts func() []string
	// Secure-pairing collaborators. All nil in production (daemon.go wires the
	// real ones); tests inject fakes so no test shells out to the tailscale CLI.
	ApplyServe  func(port int) error
	ClearServe  func() error
	QueryTS     func() mobilebridge.TailscaleInfo
	ServeTarget func() int
	// ProbePublic checks that the stored public URL is really served; nil uses
	// a real HTTPS/HTTP request. Tests inject a stub.
	ProbePublic func(ctx context.Context, ep mobilebridge.Endpoint) bool
	epCache     endpointCache

	// HostID is this machine's stable identity, echoed into the pairing code so
	// the phone can verify every endpoint it races against the machine it
	// actually paired with.
	HostID string

	// TLSPin is the gateway certificate's public-key fingerprint, stamped on
	// the lan and tailscale endpoints so the phone can pin it. Empty when the
	// gateway serves plain http only.
	TLSPin string

	// ManagedOff reports that the managed (MDM) policy switches mobile off.
	// While it is true the bridge refuses every enable path and never restores
	// at boot; nil means no policy.
	ManagedOff func() bool

	// Tunnel is the managed remote-access connector. Nil when remote access is
	// unavailable — no usable cloudflared, or the feature switched off — in
	// which case the bridge behaves exactly as the LAN-only version did.
	Tunnel TunnelController

	// ResolveTunnel looks for a connector binary again, for the case where one
	// was installed after the daemon started. Resolution otherwise happens once
	// at boot, so a cloudflared installed from Connect Mobile stayed invisible
	// until Tallpine was restarted. Nil in tests that set Tunnel directly.
	ResolveTunnel func() TunnelController

	// KeepAwake holds the machine awake while the bridge is on and the user has
	// opted in. Nil where the platform has no way to do it (everything but
	// macOS), which Status reports as unsupported.
	KeepAwake KeepAwakeController

	// OnBrowserRemoteAccessDisabled synchronously revokes live streams when the
	// desktop turns the additional browser-access gate off. It is nil until the
	// browser-live service is wired.
	OnBrowserRemoteAccessDisabled func()

	// transitionMu serializes every operation that changes persisted bridge
	// state, listener state, or connector state. Status deliberately does not
	// take it, so a slow listener or connector operation cannot freeze polling.
	pairing pairingCodeState

	transitionMu sync.Mutex

	// Guards Tunnel, which ensureTunnel may replace while HTTP handlers read it.
	tunnelMu sync.RWMutex
	// tunnelResolving is closed once ResolveTunnelInBackground's lookup ends.
	// Nil when no background lookup was started.
	tunnelResolving chan struct{}

	// stateMu protects only the short-lived in-memory status snapshot below. No
	// listener, filesystem, or connector call may run while it is held.
	stateMu sync.RWMutex
	// serveErr records the last Apply/Clear failure for Status.
	serveErr error
}
