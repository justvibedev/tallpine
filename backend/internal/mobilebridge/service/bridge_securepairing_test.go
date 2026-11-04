package service

import (
	"errors"
	"path/filepath"
	"testing"

	"tallpine.eu/tallpine/backend/internal/mobilebridge"
)

// The bridge falls back to an ephemeral port when its default is taken, so the
// proxy must be pointed at the port Start actually returned — not DefaultPort.
// This is the stale-port regression this feature exists to avoid.
func TestSecurePairingAppliesActualBoundPort(t *testing.T) {
	// Start returns the actual ephemeral-fallback port (54014); BoundPort is
	// deliberately stale (3011) so the test fails if the implementation
	// re-reads BoundPort instead of using the port Start returned.
	lan := &portOverrideLAN{startPort: 54014, boundPortStale: 3011}
	var applied []int
	b := &BridgeService{
		LAN:         lan,
		ConfigPath:  filepath.Join(t.TempDir(), "mobile", "config.json"),
		DefaultPort: 3011,
		ApplyServe:  func(port int) error { applied = append(applied, port); return nil },
		ClearServe:  func() error { return nil },
		QueryTS:     func() mobilebridge.TailscaleInfo { return tsUp },
		ServeTarget: func() int { return 54014 },
	}
	if _, err := b.SetSecurePairing(true); err != nil {
		t.Fatalf("SetSecurePairing: %v", err)
	}
	if _, err := b.Enable(); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if len(applied) == 0 || applied[len(applied)-1] != 54014 {
		t.Errorf("applied = %v, want the bound port 54014", applied)
	}
}

func TestSecurePairingStatusActive(t *testing.T) {
	b := newSecureBridge(t, tsUp, func() int { return 3011 })
	if _, err := b.SetSecurePairing(true); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Enable(); err != nil {
		t.Fatal(err)
	}
	sp := b.Status().SecurePairing
	if !sp.Enabled || !sp.Available || !sp.Active {
		t.Fatalf("securePairing = %+v, want enabled/available/active", sp)
	}
	if sp.Host != "mac.tailnet-example.ts.net" || sp.Port != 443 {
		t.Errorf("host/port = %q/%d", sp.Host, sp.Port)
	}
	if sp.Reason != "" {
		t.Errorf("Reason = %q, want empty when available", sp.Reason)
	}
}

func TestSecurePairingReasons(t *testing.T) {
	cases := []struct {
		name   string
		info   mobilebridge.TailscaleInfo
		target func() int
		want   string
	}{
		{"no cli", mobilebridge.TailscaleInfo{}, func() int { return 0 }, "no_cli"},
		{"no certs", mobilebridge.TailscaleInfo{Name: "h.tail1.ts.net"}, func() int { return 0 }, "no_certs"},
		{"port mismatch", tsUp, func() int { return 9999 }, "port_mismatch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := newSecureBridge(t, tc.info, tc.target)
			if _, err := b.SetSecurePairing(true); err != nil {
				t.Fatal(err)
			}
			if _, err := b.Enable(); err != nil {
				t.Fatal(err)
			}
			if got := b.Status().SecurePairing.Reason; got != tc.want {
				t.Errorf("Reason = %q, want %q", got, tc.want)
			}
		})
	}
}

// A serve failure must leave the bridge running in plaintext mode rather than
// taking pairing down entirely.
func TestSecurePairingServeFailureKeepsBridgeUp(t *testing.T) {
	b := newSecureBridge(t, tsUp, func() int { return 0 })
	b.ApplyServe = func(int) error { return errors.New("port 443 in use") }
	if _, err := b.SetSecurePairing(true); err != nil {
		t.Fatal(err)
	}
	res, err := b.Enable()
	if err != nil {
		t.Fatalf("Enable must succeed despite serve failure: %v", err)
	}
	if !res.Enabled {
		t.Error("bridge not enabled")
	}
	if got := b.Status().SecurePairing.Reason; got != "serve_failed" {
		t.Errorf("Reason = %q, want serve_failed", got)
	}
}

// Turning the mode off must tear the proxy down, or it keeps pointing at a
// bridge the user believes is private.
func TestSecurePairingOffClearsProxy(t *testing.T) {
	cleared := 0
	b := newSecureBridge(t, tsUp, func() int { return 3011 })
	b.ClearServe = func() error { cleared++; return nil }
	if _, err := b.SetSecurePairing(false); err != nil || cleared != 0 {
		t.Fatalf("turning off unused secure pairing cleared an unrelated Serve route: clears=%d err=%v", cleared, err)
	}
	if _, err := b.SetSecurePairing(true); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SetSecurePairing(false); err != nil {
		t.Fatal(err)
	}
	if cleared != 1 {
		t.Errorf("Clear called %d times, want 1", cleared)
	}
}

// If Clear fails when turning the mode off, the flag is already persisted
// off (so the API call must still succeed), but the proxy may still be live
// — Status must surface that as clear_failed rather than silently reporting
// disabled.
func TestSecurePairingOffClearFailureReported(t *testing.T) {
	b := newSecureBridge(t, tsUp, func() int { return 3011 })
	if _, err := b.SetSecurePairing(true); err != nil {
		t.Fatal(err)
	}
	b.ClearServe = func() error { return errors.New("tailscale serve clear: exit 1") }
	if _, err := b.SetSecurePairing(false); err != nil {
		t.Fatalf("SetSecurePairing(false) must not return an error: %v", err)
	}
	sp := b.Status().SecurePairing
	if sp.Enabled {
		t.Error("Enabled = true, want false")
	}
	if sp.Reason != "clear_failed" {
		t.Errorf("Reason = %q, want clear_failed", sp.Reason)
	}
}

var tsUp = mobilebridge.TailscaleInfo{
	Name:         "mac.tailnet-example.ts.net",
	CertsEnabled: true,
}

// portOverrideLAN is fakeLAN with a caller-chosen bound port, so tests can
// exercise the ephemeral-fallback case. Start and BoundPort deliberately
// return different values: Start returns the freshly bound ephemeral port,
// while BoundPort returns a stale port a caller might have cached from
// before the fallback. Do not "fix" this by making them agree — the gap is
// what lets TestSecurePairingAppliesActualBoundPort catch a regression to
// re-reading b.LAN.BoundPort() instead of using the port Start returned.
type portOverrideLAN struct {
	fakeLAN
	startPort      int
	boundPortStale int
}

func (f *portOverrideLAN) BoundPort() int         { return f.boundPortStale }
func (f *portOverrideLAN) Start(int) (int, error) { f.running = true; return f.startPort, nil }

func newSecureBridge(t *testing.T, info mobilebridge.TailscaleInfo, target func() int) *BridgeService {
	t.Helper()
	return &BridgeService{
		LAN:         &fakeLAN{},
		ConfigPath:  filepath.Join(t.TempDir(), "mobile", "config.json"),
		DefaultPort: 3011,
		ApplyServe:  func(int) error { return nil },
		ClearServe:  func() error { return nil },
		QueryTS:     func() mobilebridge.TailscaleInfo { return info },
		ServeTarget: target,
	}
}

// `tailscale serve --https=443 off` is node-global: it removes whatever is on
// :443, not merely what Tallpine put there. Disabling a bridge that never enabled
// secure pairing must therefore leave the tailnet proxy strictly alone, or Tallpine
// silently destroys a serve route its user configured for themselves — or one
// belonging to another Tallpine instance on the same node.
func TestDisableLeavesServeAloneWhenSecurePairingNeverEnabled(t *testing.T) {
	cleared := 0
	b := newSecureBridge(t, tsUp, func() int { return 3011 })
	b.ClearServe = func() error { cleared++; return nil }
	if _, err := b.Enable(); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if err := b.Disable(); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if cleared != 0 {
		t.Errorf("clearServe called %d times, want 0 — Tallpine must not touch a proxy it never installed", cleared)
	}
}

// When Tallpine did install the proxy, disabling must still remove it.
func TestDisableClearsServeWhenSecurePairingEnabled(t *testing.T) {
	cleared := 0
	b := newSecureBridge(t, tsUp, func() int { return 3011 })
	b.ClearServe = func() error { cleared++; return nil }
	if _, err := b.Enable(); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if _, err := b.SetSecurePairing(true); err != nil {
		t.Fatalf("set secure: %v", err)
	}
	if err := b.Disable(); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if cleared != 1 {
		t.Errorf("clearServe called %d times, want 1", cleared)
	}
}

// `tailscale serve --bg` outlives this process, so a graceful shutdown that
// stops only the listener leaves the tailnet routing to a local port with
// nothing authenticated behind it. Whatever binds that port next would be
// published to the tailnet in Tallpine's place.
func TestShutdownServeClearsProxyWhenSecurePairingEnabled(t *testing.T) {
	cleared := 0
	b := newSecureBridge(t, tsUp, func() int { return 3011 })
	b.ClearServe = func() error { cleared++; return nil }
	if _, err := b.Enable(); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if _, err := b.SetSecurePairing(true); err != nil {
		t.Fatalf("set secure: %v", err)
	}
	b.ShutdownServe()
	if cleared != 1 {
		t.Errorf("clearServe called %d times on shutdown, want 1", cleared)
	}
}

// The preference must survive shutdown so boot restore re-applies the proxy
// against the next bound port without the user re-toggling anything.
func TestShutdownServeKeepsSecurePairingPreference(t *testing.T) {
	b := newSecureBridge(t, tsUp, func() int { return 3011 })
	if _, err := b.Enable(); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if _, err := b.SetSecurePairing(true); err != nil {
		t.Fatalf("set secure: %v", err)
	}
	b.ShutdownServe()
	st, err := mobilebridge.Load(b.ConfigPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !st.SecurePairing {
		t.Error("SecurePairing was cleared by shutdown; boot restore would not re-apply the proxy")
	}
}

// A shutdown with the bridge off, or with secure pairing never enabled, must
// not issue the node-global clear either.
func TestShutdownServeNoopWhenNotOwned(t *testing.T) {
	for name, setup := range map[string]func(*BridgeService){
		"bridge disabled":         func(b *BridgeService) {},
		"secure pairing never on": func(b *BridgeService) { _, _ = b.Enable() },
	} {
		t.Run(name, func(t *testing.T) {
			cleared := 0
			b := newSecureBridge(t, tsUp, func() int { return 3011 })
			b.ClearServe = func() error { cleared++; return nil }
			setup(b)
			b.ShutdownServe()
			if cleared != 0 {
				t.Errorf("clearServe called %d times, want 0", cleared)
			}
		})
	}
}
