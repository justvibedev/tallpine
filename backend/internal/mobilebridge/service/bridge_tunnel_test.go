package service

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"tallpine.eu/tallpine/backend/internal/mobilebridge"
)

func TestMobileEnableStartsTheTunnelOnTheBoundPort(t *testing.T) {
	// The connector must target the port the listener actually bound, not the
	// configured default: Start falls back to an ephemeral port when the
	// default is taken, and a connector pointed at the wrong port tunnels
	// nothing.
	tun := &fakeTunnel{}
	b := &BridgeService{
		LAN:                &fakeLAN{},
		ConfigPath:         filepath.Join(t.TempDir(), "mobile", "config.json"),
		DefaultPort:        3011,
		PickLANHosts:       func() []string { return []string{"192.168.1.42"} },
		PickTailscaleHosts: func() []string { return nil },
		Tunnel:             tun,
	}
	optInQuickTunnel(t, b.ConfigPath)
	if _, err := b.Enable(); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if tun.startedOn != 3011 {
		t.Fatalf("tunnel started on %d, want the bound port 3011", tun.startedOn)
	}
}

type stopErrorLAN struct {
	fakeLAN
	err          error
	stillRunning bool
}

func (f *stopErrorLAN) Stop(context.Context) error {
	f.stopCalls++
	f.running = f.stillRunning
	return f.err
}

func TestMobileDisableFinishesCleanupAfterStopErrorWhenListenerStopped(t *testing.T) {
	stopErr := errors.New("shutdown deadline exceeded")
	path := filepath.Join(t.TempDir(), "mobile.json")
	if err := mobilebridge.Save(path, mobilebridge.State{
		Enabled:       true,
		Password:      "paired-password",
		LastPort:      3011,
		SecurePairing: true,
	}); err != nil {
		t.Fatal(err)
	}
	cleared := 0
	lan := &stopErrorLAN{
		fakeLAN: fakeLAN{running: true, hash: mobilebridge.HashPassword("paired-password")},
		err:     stopErr,
	}
	bridge := &BridgeService{
		LAN:         lan,
		ConfigPath:  path,
		ClearServe:  func() error { cleared++; return nil },
		DefaultPort: 3011,
	}

	if err := bridge.Disable(); !errors.Is(err, stopErr) {
		t.Fatalf("Disable error = %v, want %v", err, stopErr)
	}
	state, err := mobilebridge.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if state.Enabled {
		t.Fatal("persisted bridge remained enabled after the listener stopped")
	}
	if cleared != 1 {
		t.Fatalf("owned secure-pairing proxy cleared %d times, want 1", cleared)
	}
}

func TestMobileDisablePreservesEnabledStateWhenStopErrorLeavesListenerRunning(t *testing.T) {
	stopErr := errors.New("listener still running")
	path := filepath.Join(t.TempDir(), "mobile.json")
	if err := mobilebridge.Save(path, mobilebridge.State{
		Enabled:       true,
		Password:      "paired-password",
		LastPort:      3011,
		SecurePairing: true,
	}); err != nil {
		t.Fatal(err)
	}
	cleared := 0
	lan := &stopErrorLAN{
		fakeLAN:      fakeLAN{running: true, hash: mobilebridge.HashPassword("paired-password")},
		err:          stopErr,
		stillRunning: true,
	}
	bridge := &BridgeService{
		LAN:         lan,
		ConfigPath:  path,
		ClearServe:  func() error { cleared++; return nil },
		DefaultPort: 3011,
	}

	if err := bridge.Disable(); !errors.Is(err, stopErr) {
		t.Fatalf("Disable error = %v, want %v", err, stopErr)
	}
	state, err := mobilebridge.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Enabled {
		t.Fatal("persisted bridge was disabled while the listener remained running")
	}
	if cleared != 0 {
		t.Fatalf("secure-pairing proxy cleared %d times while listener remained live", cleared)
	}
}

func TestMobileDisableStopsTheTunnel(t *testing.T) {
	// Leaving a public tunnel up after the user turned Connect Mobile off would
	// keep the machine reachable from the internet with the UI saying it is not.
	tun := &fakeTunnel{}
	b := &BridgeService{
		LAN:                &fakeLAN{},
		ConfigPath:         filepath.Join(t.TempDir(), "mobile", "config.json"),
		DefaultPort:        3011,
		PickLANHosts:       func() []string { return []string{"192.168.1.42"} },
		PickTailscaleHosts: func() []string { return nil },
		Tunnel:             tun,
	}
	if _, err := b.Enable(); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if err := b.Disable(); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if tun.stops != 1 {
		t.Fatalf("tunnel stopped %d times, want 1", tun.stops)
	}
}

func TestMobileStatusIncludesAReadyTunnelEndpoint(t *testing.T) {
	tun := &fakeTunnel{
		endpoint: &mobilebridge.TunnelEndpoint{Ready: true, Hostname: "abc.trycloudflare.com"},
		status:   mobilebridge.TunnelStatus{Running: true, Ready: true, Hostname: "abc.trycloudflare.com"},
	}
	b := &BridgeService{
		LAN:                &fakeLAN{running: true},
		ConfigPath:         filepath.Join(t.TempDir(), "mobile", "config.json"),
		DefaultPort:        3011,
		PickLANHosts:       func() []string { return []string{"192.168.1.42"} },
		PickTailscaleHosts: func() []string { return nil },
		Tunnel:             tun,
	}
	if _, err := b.Enable(); err != nil {
		t.Fatalf("enable: %v", err)
	}

	got := b.Status()
	last := got.Endpoints[len(got.Endpoints)-1]
	if last.Kind != mobilebridge.KindTunnel || last.Host != "abc.trycloudflare.com" {
		t.Fatalf("last endpoint = %+v, want the tunnel", last)
	}
	if !got.Tunnel.Ready {
		t.Error("Tunnel status not surfaced; the desktop cannot show progress")
	}
}

func TestMobileStatusOmitsATunnelThatIsNotReady(t *testing.T) {
	// Still settling. The desktop should say "preparing", and the QR must not
	// carry an address that answers 530.
	tun := &fakeTunnel{status: mobilebridge.TunnelStatus{Running: true}}
	b := &BridgeService{
		LAN:                &fakeLAN{running: true},
		ConfigPath:         filepath.Join(t.TempDir(), "mobile", "config.json"),
		DefaultPort:        3011,
		PickLANHosts:       func() []string { return []string{"192.168.1.42"} },
		PickTailscaleHosts: func() []string { return nil },
		Tunnel:             tun,
	}
	if _, err := b.Enable(); err != nil {
		t.Fatalf("enable: %v", err)
	}

	for _, e := range b.Status().Endpoints {
		if e.Kind == mobilebridge.KindTunnel {
			t.Fatalf("advertised %+v while the tunnel was not ready", e)
		}
	}
}

func TestMobileWorksWithNoTunnelConfigured(t *testing.T) {
	// Remote access unavailable (no cloudflared, or the feature off) must leave
	// the LAN bridge behaving exactly as it did before.
	b := &BridgeService{
		LAN:                &fakeLAN{running: true},
		ConfigPath:         filepath.Join(t.TempDir(), "mobile", "config.json"),
		DefaultPort:        3011,
		PickLANHosts:       func() []string { return []string{"192.168.1.42"} },
		PickTailscaleHosts: func() []string { return nil },
		TLSPin:             "ab12",
	}
	if _, err := b.Enable(); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if got := b.Status(); len(got.Endpoints) != 1 || got.Endpoints[0].Kind != mobilebridge.KindLAN {
		t.Fatalf("endpoints = %+v, want just the LAN one", got.Endpoints)
	}
	if err := b.Disable(); err != nil {
		t.Fatalf("disable: %v", err)
	}
}

// The desktop builds the pairing code, and the code must carry the machine's
// identity so the phone can verify every endpoint it later races. Without it
// here the renderer would have to make a second call to /api/v1/identity just
// to draw a QR.
func TestMobileStatusCarriesTheHostIdentity(t *testing.T) {
	b := &BridgeService{
		LAN:                &fakeLAN{running: true},
		ConfigPath:         filepath.Join(t.TempDir(), "mobile", "config.json"),
		DefaultPort:        3011,
		HostID:             "h_b3e07f31",
		PickLANHosts:       func() []string { return []string{"192.168.1.42"} },
		PickTailscaleHosts: func() []string { return nil },
	}
	if _, err := b.Enable(); err != nil {
		t.Fatalf("enable: %v", err)
	}

	if got := b.Status().HostID; got != "h_b3e07f31" {
		t.Fatalf("HostID = %q want h_b3e07f31", got)
	}
}

// Found by running the daemon: with Connect Mobile off there is no bound port,
// so every endpoint was advertised as host:0. A phone would dutifully race
// addresses that cannot work. Nothing is reachable until the bridge is up, so
// the honest answer is an empty list.
func TestMobileAdvertisesNothingWhileTheBridgeIsOff(t *testing.T) {
	b := &BridgeService{
		LAN:                &fakeLAN{running: false},
		ConfigPath:         filepath.Join(t.TempDir(), "mobile", "config.json"),
		DefaultPort:        3011,
		PickLANHosts:       func() []string { return []string{"192.168.1.42"} },
		PickTailscaleHosts: func() []string { return []string{"100.72.46.7"} },
		QueryTS:            func() mobilebridge.TailscaleInfo { return mobilebridge.TailscaleInfo{} },
	}

	if got := b.AdvertisedEndpoints(); len(got) != 0 {
		t.Fatalf("advertised %+v while the bridge was disabled", got)
	}
	if got := b.Status().Endpoints; len(got) != 0 {
		t.Fatalf("status advertised %+v while the bridge was disabled", got)
	}
}

// Found by running the desktop app: enabling Connect Mobile started the
// connector, but restarting the daemon restored only the LAN listener. Remote
// access then stayed silently off — the UI showed the bridge enabled while the
// tunnel never came back — until the user toggled it off and on.
//
// A restart does not go through enableWithPasswordLocked (there is no password to
// rotate), so RestoreOnBoot has to mirror its post-Start work.
func TestMobileRestoreOnBootStartsTheTunnel(t *testing.T) {
	tun := &fakeTunnel{}
	b := &BridgeService{
		LAN:                &fakeLAN{},
		ConfigPath:         filepath.Join(t.TempDir(), "mobile", "config.json"),
		DefaultPort:        3011,
		PickLANHosts:       func() []string { return []string{"192.168.1.42"} },
		PickTailscaleHosts: func() []string { return nil },
		Tunnel:             tun,
	}

	if err := b.RestoreOnBoot(mobilebridge.State{Enabled: true, QuickTunnel: true, Password: "pw", LastPort: 3011}); err != nil {
		t.Fatalf("restore: %v", err)
	}

	if tun.startedOn != 3011 {
		t.Fatalf("tunnel started on %d, want the restored port 3011", tun.startedOn)
	}
}

func TestMobileRestoreOnBootWithoutATunnelIsHarmless(t *testing.T) {
	b := &BridgeService{
		LAN:                &fakeLAN{},
		ConfigPath:         filepath.Join(t.TempDir(), "mobile", "config.json"),
		DefaultPort:        3011,
		PickLANHosts:       func() []string { return []string{"192.168.1.42"} },
		PickTailscaleHosts: func() []string { return nil },
	}
	if err := b.RestoreOnBoot(mobilebridge.State{Enabled: true, Password: "pw", LastPort: 3011}); err != nil {
		t.Fatalf("restore: %v", err)
	}
}

// A cloudflared process outlives the daemon that spawned it, so quitting
// without stopping it leaves a public hostname pointing at a port that no
// longer has the authenticated LAN listener behind it. Reaping on the next
// boot cleans it up eventually, but "eventually" is whenever the user happens
// to reopen the app — until then the process runs and the hostname resolves.
func TestShutdownTunnelStopsTheConnector(t *testing.T) {
	dir := t.TempDir()
	tun := &fakeTunnel{}
	b := &BridgeService{ConfigPath: filepath.Join(dir, "mobile.json"), Tunnel: tun}

	b.ShutdownTunnel()

	if tun.stops != 1 {
		t.Fatalf("connector stops = %d, want 1", tun.stops)
	}
}

// Remote access is optional: without cloudflared installed there is no
// connector, and shutdown must not panic on the way out.
func TestShutdownTunnelWithoutAConnector(t *testing.T) {
	dir := t.TempDir()
	b := &BridgeService{ConfigPath: filepath.Join(dir, "mobile.json")}

	b.ShutdownTunnel()
}

// Remote access is optional: without cloudflared there is no connector at all.
// A zero TunnelStatus made that indistinguishable from "not started yet", so
// the desktop showed a normal QR and the user had no way to learn that
// connecting from cellular was simply unavailable on this machine.
func TestTunnelStatusReportsWhenRemoteAccessIsUnsupported(t *testing.T) {
	dir := t.TempDir()
	b := &BridgeService{ConfigPath: filepath.Join(dir, "mobile.json")}

	st := b.tunnelStatus()

	if st.Supported {
		t.Fatal("no connector configured, so remote access is not supported")
	}
}

func TestTunnelStatusReportsSupportedWhenAConnectorExists(t *testing.T) {
	dir := t.TempDir()
	b := &BridgeService{ConfigPath: filepath.Join(dir, "mobile.json"), Tunnel: &fakeTunnel{}}

	if !b.tunnelStatus().Supported {
		t.Fatal("a configured connector means remote access is supported")
	}
}

// Resolution happens once at daemon start, so a cloudflared installed from
// Connect Mobile was invisible until Tallpine restarted — the user pressed Install,
// watched it succeed, and remote access stayed off with no explanation.
// Enabling is the natural moment to look again.
func TestEnableResolvesAConnectorInstalledSinceBoot(t *testing.T) {
	dir := t.TempDir()
	tun := &fakeTunnel{}
	resolved := 0
	b := &BridgeService{
		ConfigPath:  filepath.Join(dir, "mobile.json"),
		LAN:         &fakeLAN{},
		DefaultPort: 3011,
		ResolveTunnel: func() TunnelController {
			resolved++
			return tun
		},
	}

	optInQuickTunnel(t, b.ConfigPath)
	if _, err := b.Enable(); err != nil {
		t.Fatalf("enable: %v", err)
	}

	if resolved != 1 {
		t.Fatalf("resolver calls = %d, want 1", resolved)
	}
	if tun.startedOn != 3011 {
		t.Fatalf("connector started on %d, want the bound port 3011", tun.startedOn)
	}
}

// A machine that still has no cloudflared must enable cleanly as a LAN-only
// bridge rather than failing: remote access is optional.
func TestEnableWithoutAConnectorStillWorks(t *testing.T) {
	dir := t.TempDir()
	b := &BridgeService{
		ConfigPath:    filepath.Join(dir, "mobile.json"),
		LAN:           &fakeLAN{},
		DefaultPort:   3011,
		ResolveTunnel: func() TunnelController { return nil },
	}

	if _, err := b.Enable(); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if b.tunnelStatus().Supported {
		t.Fatal("no connector resolved, so remote access is not supported")
	}
}

// Installing cloudflared used to re-enable the bridge so the daemon would look
// for the new binary. Enable always mints a fresh password, so that silently
// invalidated the phone already paired — the user installed remote access and
// lost the connection they were setting it up for.
func TestStartRemoteAccessKeepsThePairedPhoneWorking(t *testing.T) {
	dir := t.TempDir()
	lan := &fakeLAN{}
	tun := &fakeTunnel{}
	b := &BridgeService{
		ConfigPath: filepath.Join(dir, "mobile.json"), LAN: lan, DefaultPort: 3011,
	}
	before, err := b.Enable()
	if err != nil {
		t.Fatalf("enable: %v", err)
	}

	// cloudflared appears only now, as an install would leave it.
	b.ResolveTunnel = func() TunnelController { return tun }
	after, err := b.StartRemoteAccess()
	if err != nil {
		t.Fatalf("StartRemoteAccess: %v", err)
	}

	if after.Password != before.Password {
		t.Fatal("password rotated: the phone paired a moment ago can no longer authenticate")
	}
	if tun.startedOn != 3011 {
		t.Fatalf("connector started on %d, want the bound port 3011", tun.startedOn)
	}
}

// Nothing to start, and nothing to break: the bridge is off, so this is a
// no-op rather than an error the UI has to special-case.
func TestStartRemoteAccessWhileDisabledDoesNothing(t *testing.T) {
	dir := t.TempDir()
	tun := &fakeTunnel{}
	b := &BridgeService{
		ConfigPath: filepath.Join(dir, "mobile.json"), LAN: &fakeLAN{}, DefaultPort: 3011,
		ResolveTunnel: func() TunnelController { return tun },
	}

	if _, err := b.StartRemoteAccess(); err != nil {
		t.Fatalf("StartRemoteAccess: %v", err)
	}
	if tun.startedOn != 0 {
		t.Fatal("connector started while the bridge is disabled")
	}
}
