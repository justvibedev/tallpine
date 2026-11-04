package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"tallpine.eu/tallpine/backend/internal/mobilebridge"
)

// fakeLAN is a minimal LANController for exercising BridgeService directly.
type fakeLAN struct {
	running   bool
	hash      string
	stopCalls int
	bindHost  string
}

func (f *fakeLAN) Start(port int) (int, error) {
	f.running = true
	f.bindHost = "0.0.0.0"
	return port, nil
}
func (f *fakeLAN) StartLoopback(port int) (int, error) {
	f.running = true
	f.bindHost = "127.0.0.1"
	return port, nil
}
func (f *fakeLAN) Stop(ctx context.Context) error {
	f.stopCalls++
	f.running = false
	return nil
}
func (f *fakeLAN) Running() bool { return f.running }
func (f *fakeLAN) BoundPort() int {
	// Mirrors the real listener: no bound port until it is actually running.
	if !f.running {
		return 0
	}
	return 3011
}
func (f *fakeLAN) SetPasswordHash(h string) { f.hash = h }
func (f *fakeLAN) PasswordHash() string     { return f.hash }

// When Save fails during a fresh enable, the listener that Start already opened
// must be torn back down and the armed hash rolled back — otherwise a LAN
// listener stays live on 0.0.0.0 while persisted state/UI say enable failed.
func TestMobileEnableRollsBackListenerWhenSaveFails(t *testing.T) {
	// A ConfigPath whose parent is a regular file makes mobilebridge.Save's
	// MkdirAll (and thus Save) fail deterministically.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	lan := &fakeLAN{}
	b := &BridgeService{LAN: lan, ConfigPath: filepath.Join(blocker, "mobile", "config.json"), DefaultPort: 3011}

	if _, err := b.Enable(); err == nil {
		t.Fatal("expected enable to fail on Save error")
	}
	if lan.Running() {
		t.Fatal("listener still running after failed enable; must be stopped")
	}
	if lan.stopCalls == 0 {
		t.Fatal("expected Stop to be called on rollback")
	}
	if lan.hash != "" {
		t.Fatalf("expected hash rolled back to empty, got %q", lan.hash)
	}
}

// Status must advertise both addresses so the renderer's LAN/Tailscale toggle
// can re-encode the pairing QR without a second round trip.
func TestMobileStatusSurfacesBothHosts(t *testing.T) {
	lan := &fakeLAN{running: true}
	b := &BridgeService{
		LAN:                lan,
		ConfigPath:         filepath.Join(t.TempDir(), "mobile", "config.json"),
		DefaultPort:        3011,
		PickLANHosts:       func() []string { return []string{"192.168.1.42"} },
		PickTailscaleHosts: func() []string { return []string{"100.72.46.7"} },
		QueryTS:            func() mobilebridge.TailscaleInfo { return mobilebridge.TailscaleInfo{} },
	}
	if _, err := b.Enable(); err != nil {
		t.Fatalf("enable: %v", err)
	}

	got := b.Status()
	if got.Host != "192.168.1.42" {
		t.Errorf("Host = %q want 192.168.1.42", got.Host)
	}
	if got.TailscaleHost != "100.72.46.7" {
		t.Errorf("TailscaleHost = %q want 100.72.46.7", got.TailscaleHost)
	}
}

// An absent Tailscale install is an empty string, not an error: the renderer
// uses "" to decide to show a hint instead of an unscannable QR.
func TestMobileStatusTailscaleHostEmptyWhenAbsent(t *testing.T) {
	b := &BridgeService{
		LAN:                &fakeLAN{running: true},
		ConfigPath:         filepath.Join(t.TempDir(), "mobile", "config.json"),
		DefaultPort:        3011,
		PickLANHosts:       func() []string { return []string{"192.168.1.42"} },
		PickTailscaleHosts: func() []string { return nil },
	}
	if _, err := b.Enable(); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if got := b.Status().TailscaleHost; got != "" {
		t.Errorf("TailscaleHost = %q want empty", got)
	}
}

// Unset pickers must fall back to the real autopickers rather than panicking on
// a nil func — production wiring in daemon.go leaves them unset. This also
// proves the call actually completed (not just "didn't panic"): the real
// AutopickLANIP/AutopickTailscaleIP paths run, and Port still comes through
// from the LAN controller untouched by that fallback.
func TestMobileStatusHostPickersDefaultWhenUnset(t *testing.T) {
	b := &BridgeService{
		LAN:         &fakeLAN{running: true},
		ConfigPath:  filepath.Join(t.TempDir(), "mobile", "config.json"),
		DefaultPort: 3011,
	}
	got := b.Status()
	if got.Port != 3011 {
		t.Errorf("Port = %d want 3011", got.Port)
	}
}

// The phone races every advertised endpoint, so Status must list all of them.
// Host/TailscaleHost keep reporting the first of each for the existing
// renderer, but they are now derived from the same lists rather than resolved
// separately, so the two can never disagree.
func TestMobileStatusAdvertisesEveryEndpoint(t *testing.T) {
	b := &BridgeService{
		LAN:                &fakeLAN{running: true},
		ConfigPath:         filepath.Join(t.TempDir(), "mobile", "config.json"),
		DefaultPort:        3011,
		PickLANHosts:       func() []string { return []string{"192.168.1.42", "10.0.0.5"} },
		PickTailscaleHosts: func() []string { return []string{"100.72.46.7"} },
		QueryTS:            func() mobilebridge.TailscaleInfo { return mobilebridge.TailscaleInfo{Name: "mac.tail1234.ts.net"} },
		TLSPin:             "ab12",
	}
	if _, err := b.Enable(); err != nil {
		t.Fatalf("enable: %v", err)
	}

	got := b.Status()

	want := []mobilebridge.Endpoint{
		{Kind: mobilebridge.KindLAN, Host: "192.168.1.42", Port: 3011, Pin: "ab12"},
		{Kind: mobilebridge.KindLAN, Host: "10.0.0.5", Port: 3011, Pin: "ab12"},
		{Kind: mobilebridge.KindTailscale, Host: "mac.tail1234.ts.net", Port: 3011, Pin: "ab12"},
		{Kind: mobilebridge.KindTailscale, Host: "100.72.46.7", Port: 3011, Pin: "ab12"},
	}
	if len(got.Endpoints) != len(want) {
		t.Fatalf("got %d endpoints %+v, want %d", len(got.Endpoints), got.Endpoints, len(want))
	}
	for i := range want {
		if got.Endpoints[i] != want[i] {
			t.Errorf("endpoint %d: got %+v want %+v", i, got.Endpoints[i], want[i])
		}
	}

	// The legacy singular fields stay in step with the lists.
	if got.Host != "192.168.1.42" {
		t.Errorf("Host = %q want the first LAN candidate", got.Host)
	}
	if got.TailscaleHost != "100.72.46.7" {
		t.Errorf("TailscaleHost = %q want the first Tailscale candidate", got.TailscaleHost)
	}
}

// No network at all is a real state. An empty list must not become a list
// containing an empty host, which the phone would try to dial.
func TestMobileStatusEndpointsEmptyWithoutNetwork(t *testing.T) {
	b := &BridgeService{
		LAN:                &fakeLAN{running: true},
		ConfigPath:         filepath.Join(t.TempDir(), "mobile", "config.json"),
		DefaultPort:        3011,
		PickLANHosts:       func() []string { return nil },
		PickTailscaleHosts: func() []string { return nil },
	}
	if _, err := b.Enable(); err != nil {
		t.Fatalf("enable: %v", err)
	}
	got := b.Status()
	if len(got.Endpoints) != 0 {
		t.Fatalf("got %+v, want no endpoints", got.Endpoints)
	}
	if got.Host != "" {
		t.Errorf("Host = %q want empty", got.Host)
	}
}

type fakeTunnel struct {
	startedOn int
	stops     int
	endpoint  *mobilebridge.TunnelEndpoint
	status    mobilebridge.TunnelStatus
}

func (f *fakeTunnel) Start(localPort int) { f.startedOn = localPort }
func (f *fakeTunnel) Stop()               { f.stops++ }
func (f *fakeTunnel) Endpoint() *mobilebridge.TunnelEndpoint {
	return f.endpoint
}
func (f *fakeTunnel) Status() mobilebridge.TunnelStatus { return f.status }

type startErrorLAN struct {
	fakeLAN
	err error
}

func (f *startErrorLAN) Start(int) (int, error) { return 0, f.err }

func TestMobileRestoreOnBootRollsBackHashWhenStartFails(t *testing.T) {
	startErr := errors.New("bind failed")
	lan := &startErrorLAN{fakeLAN: fakeLAN{hash: "previous-hash"}, err: startErr}
	bridge := &BridgeService{LAN: lan}

	err := bridge.RestoreOnBoot(mobilebridge.State{Enabled: true, Password: "restored-password", LastPort: 3011})
	if !errors.Is(err, startErr) {
		t.Fatalf("RestoreOnBoot error = %v, want %v", err, startErr)
	}
	if got := lan.PasswordHash(); got != "previous-hash" {
		t.Fatalf("password hash after failed restore = %q, want previous-hash", got)
	}
}
