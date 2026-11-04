package service

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"tallpine.eu/tallpine/backend/internal/mobilebridge"
)

func featureBridge(t *testing.T, tun *fakeTunnel) *BridgeService {
	t.Helper()
	b := &BridgeService{
		LAN:                &fakeLAN{},
		ConfigPath:         filepath.Join(t.TempDir(), "mobile", "config.json"),
		DefaultPort:        3011,
		PickLANHosts:       func() []string { return []string{"192.168.1.42"} },
		PickTailscaleHosts: func() []string { return nil },
	}
	if tun != nil {
		b.Tunnel = tun // avoid a typed-nil interface
	}
	return b
}

func TestFeatureIsOffByDefault(t *testing.T) {
	st := featureBridge(t, nil).Status()
	if st.FeatureEnabled || st.Enabled || st.Password != "" || len(st.Endpoints) != 0 || st.QuickTunnel || !st.NoPublicTunnel {
		t.Fatalf("fresh status = %+v", st)
	}
}

func TestEnableDoesNotStartTunnelByDefault(t *testing.T) {
	tun := &fakeTunnel{}
	b := featureBridge(t, tun)
	if _, err := b.SetFeatureEnabled(true); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Enable(); err != nil {
		t.Fatal(err)
	}
	if tun.startedOn != 0 {
		t.Fatalf("tunnel started on %d without the quick-tunnel opt-in", tun.startedOn)
	}
}

func TestQuickTunnelOptInStartsAndStopsTunnel(t *testing.T) {
	tun := &fakeTunnel{}
	b := featureBridge(t, tun)
	_, _ = b.SetFeatureEnabled(true)
	if _, err := b.Enable(); err != nil {
		t.Fatal(err)
	}
	st, err := b.SetQuickTunnel(true)
	if err != nil || !st.QuickTunnel || st.NoPublicTunnel || tun.startedOn != 3011 {
		t.Fatalf("opt in: st=%+v err=%v started=%d", st, err, tun.startedOn)
	}
	st, err = b.SetQuickTunnel(false)
	if err != nil || st.QuickTunnel || tun.stops != 1 {
		t.Fatalf("opt out: st=%+v err=%v stops=%d", st, err, tun.stops)
	}
}

func TestFeatureOffTearsEverythingDown(t *testing.T) {
	tun := &fakeTunnel{}
	b := featureBridge(t, tun)
	revoked := 0
	b.OnBrowserRemoteAccessDisabled = func() { revoked++ }
	_, _ = b.SetFeatureEnabled(true)
	_, _ = b.SetQuickTunnel(true)
	if _, err := b.Enable(); err != nil {
		t.Fatal(err)
	}
	st, err := b.SetFeatureEnabled(false)
	if err != nil {
		t.Fatal(err)
	}
	if st.FeatureEnabled || st.Enabled || b.LAN.Running() || tun.stops == 0 || revoked != 1 {
		t.Fatalf("flag off left things running: st=%+v running=%v stops=%d revoked=%d", st, b.LAN.Running(), tun.stops, revoked)
	}
	saved, _ := mobilebridge.Load(b.ConfigPath)
	if saved.FeatureEnabled || saved.Enabled {
		t.Fatalf("persisted state = %+v", saved)
	}
}

func TestEnablePreservesFeatureAndPublicURL(t *testing.T) {
	b := featureBridge(t, nil)
	_, _ = b.SetFeatureEnabled(true)
	if _, err := b.SetPublicBaseURL("https://tallpine.example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Enable(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Regenerate(); err != nil {
		t.Fatal(err)
	}
	saved, _ := mobilebridge.Load(b.ConfigPath)
	if !saved.FeatureEnabled || saved.PublicBaseURL != "https://tallpine.example.com:443" {
		t.Fatalf("state lost across enable/regenerate: %+v", saved)
	}
}

func TestPublicBaseURLIsAdvertisedInsteadOfAnyDirectory(t *testing.T) {
	b := featureBridge(t, nil)
	_, _ = b.SetFeatureEnabled(true)
	if _, err := b.Enable(); err != nil {
		t.Fatal(err)
	}
	b.ProbePublic = func(context.Context, mobilebridge.Endpoint) bool { return true }
	st, err := b.SetPublicBaseURL("https://mac.tail1234.ts.net:8443")
	if err != nil {
		t.Fatal(err)
	}
	var got *mobilebridge.Endpoint
	for i := range st.Endpoints {
		if st.Endpoints[i].Kind == mobilebridge.KindRelay {
			got = &st.Endpoints[i]
		}
	}
	if got == nil || got.Host != "mac.tail1234.ts.net" || got.Port != 8443 || !got.Secure {
		t.Fatalf("endpoints = %+v", st.Endpoints)
	}
	if n := len(b.AdvertisedEndpoints()); n != len(st.Endpoints) {
		t.Fatalf("refresh route (%d) and status (%d) disagree", n, len(st.Endpoints))
	}
	st, err = b.SetPublicBaseURL("")
	if err != nil || st.PublicBaseURL != "" {
		t.Fatalf("clear: %+v %v", st, err)
	}
	for _, e := range st.Endpoints {
		if e.Kind == mobilebridge.KindRelay {
			t.Fatalf("cleared URL still advertised: %+v", e)
		}
	}
}

func TestPublicBaseURLRejectsPlainHTTP(t *testing.T) {
	for _, raw := range []string{"http://tallpine.example.com", "http://mac.tail1234.ts.net:3011", "http://192.168.1.5:3011"} {
		b := featureBridge(t, nil)
		_, err := b.SetPublicBaseURL(raw)
		if !errors.Is(err, mobilebridge.ErrInvalidPublicBaseURL) {
			t.Fatalf("%s: err = %v", raw, err)
		}
		if saved, _ := mobilebridge.Load(b.ConfigPath); saved.PublicBaseURL != "" {
			t.Fatalf("rejected URL was persisted: %q", saved.PublicBaseURL)
		}
	}
}

// An http URL saved by an older build is never advertised to phones; status
// reports it unreachable so the user replaces it.
func TestStoredPlainHTTPPublicURLIsNotAdvertised(t *testing.T) {
	b := featureBridge(t, nil)
	_, _ = b.SetFeatureEnabled(true)
	if _, err := b.Enable(); err != nil {
		t.Fatal(err)
	}
	st, _ := mobilebridge.Load(b.ConfigPath)
	st.PublicBaseURL = "http://mac.tail1234.ts.net:3011"
	if err := mobilebridge.Save(b.ConfigPath, st); err != nil {
		t.Fatal(err)
	}
	b.ProbePublic = func(context.Context, mobilebridge.Endpoint) bool { return true }
	for _, e := range b.AdvertisedEndpoints() {
		if e.Kind == mobilebridge.KindRelay {
			t.Fatalf("legacy http URL advertised: %+v", e)
		}
	}
	if got := b.Status().PublicURLState; got != publicURLUnreachable {
		t.Fatalf("public URL state = %q, want %q", got, publicURLUnreachable)
	}
}

// tunnelPortLAN gives cloudflared its own port, like the real listener.
type tunnelPortLAN struct {
	*fakeLAN
	closed *int
}

func (tunnelPortLAN) OpenTunnelPort() int { return 4242 }
func (l tunnelPortLAN) CloseTunnelPort() {
	if l.closed != nil {
		*l.closed++
	}
}

// The loopback port must close on every path that turns the quick tunnel off.
func TestTunnelPortClosesWhenQuickTunnelTurnsOff(t *testing.T) {
	closed := 0
	b := featureBridge(t, &fakeTunnel{})
	b.LAN = tunnelPortLAN{&fakeLAN{}, &closed}
	_, _ = b.SetFeatureEnabled(true)
	if _, err := b.Enable(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SetQuickTunnel(true); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SetQuickTunnel(false); err != nil || closed != 1 {
		t.Fatalf("SetQuickTunnel(false): err=%v closed=%d, want 1", err, closed)
	}
	if _, err := b.SetQuickTunnel(true); err != nil {
		t.Fatal(err)
	}
	if _, err := b.EnableLANOnly(); err != nil || closed != 2 {
		t.Fatalf("EnableLANOnly: err=%v closed=%d, want 2", err, closed)
	}
}

// CF-Connecting-IP is believed only on the tunnel port, so every path that
// starts the connector must use it, not the main port.
func TestConnectorStartsOnTunnelPortOnEveryPath(t *testing.T) {
	tun := &fakeTunnel{}
	b := featureBridge(t, tun)
	b.LAN = tunnelPortLAN{fakeLAN: &fakeLAN{}}
	_, _ = b.SetFeatureEnabled(true)
	if _, err := b.Enable(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SetQuickTunnel(true); err != nil || tun.startedOn != 4242 {
		t.Fatalf("opt in: err=%v started=%d, want 4242", err, tun.startedOn)
	}
	if err := b.Disable(); err != nil {
		t.Fatal(err)
	}
	tun.startedOn = 0
	if _, err := b.Enable(); err != nil || tun.startedOn != 4242 {
		t.Fatalf("enable: err=%v started=%d, want 4242", err, tun.startedOn)
	}
	tun.startedOn = 0
	if err := b.RestoreOnBoot(mobilebridge.State{Enabled: true, QuickTunnel: true, Password: "pw", LastPort: 3011}); err != nil || tun.startedOn != 4242 {
		t.Fatalf("boot: err=%v started=%d, want 4242", err, tun.startedOn)
	}
}

// A stored public URL nothing serves (cleared Tailscale Serve, dead proxy) must
// not be advertised: the phone would race a dead relay on every connect.
func TestDeadPublicBaseURLIsNotAdvertised(t *testing.T) {
	b := featureBridge(t, nil)
	_, _ = b.SetFeatureEnabled(true)
	if _, err := b.Enable(); err != nil {
		t.Fatal(err)
	}
	live := false
	b.ProbePublic = func(context.Context, mobilebridge.Endpoint) bool { return live }
	st, err := b.SetPublicBaseURL("https://mac.tail1234.ts.net")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range st.Endpoints {
		if e.Kind == mobilebridge.KindRelay {
			t.Fatalf("dead public URL advertised: %+v", st.Endpoints)
		}
	}
	if st.PublicURLState != "unreachable" || st.PublicBaseURL == "" {
		t.Fatalf("state = %q url = %q, want unreachable with URL kept", st.PublicURLState, st.PublicBaseURL)
	}
	for _, e := range b.AdvertisedEndpoints() {
		if e.Kind == mobilebridge.KindRelay {
			t.Fatalf("refresh route advertises dead relay: %+v", e)
		}
	}
	// Once it answers (after the cache window) it is advertised and reported ok.
	live = true
	b.epCache = endpointCache{}
	st = b.Status()
	if st.PublicURLState != "ok" {
		t.Fatalf("state = %q, want ok", st.PublicURLState)
	}
	found := false
	for _, e := range st.Endpoints {
		found = found || e.Kind == mobilebridge.KindRelay
	}
	if !found {
		t.Fatalf("live public URL missing: %+v", st.Endpoints)
	}
}

func TestPublicURLStateNotConfigured(t *testing.T) {
	b := featureBridge(t, nil)
	_, _ = b.SetFeatureEnabled(true)
	if got := b.Status().PublicURLState; got != "not_configured" {
		t.Fatalf("state = %q", got)
	}
}
