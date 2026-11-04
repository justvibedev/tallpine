package daemon

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/mobilebridge"
	bridgesvc "tallpine.eu/tallpine/backend/internal/mobilebridge/service"
)

// fakeLAN is a minimal httpd.LANController fake for exercising
// restoreMobileOnBoot without a real listener.
type fakeLAN struct {
	started bool
	hash    string
	port    int
	// returnPort, when non-zero, is what Start returns instead of the port it
	// was asked for — simulating LANManager's ephemeral-port fallback when the
	// requested port is already taken (e.g. by another Tallpine instance). Left zero,
	// Start behaves like a normal listener and echoes the requested port back.
	returnPort int
}

func (f *fakeLAN) Start(port int) (int, error) {
	f.started = true
	if f.returnPort != 0 {
		f.port = f.returnPort
		return f.returnPort, nil
	}
	f.port = port
	return port, nil
}
func (f *fakeLAN) StartLoopback(port int) (int, error) {
	return f.Start(port)
}
func (f *fakeLAN) Stop(ctx context.Context) error { return nil }
func (f *fakeLAN) Running() bool                  { return f.started }
func (f *fakeLAN) BoundPort() int                 { return f.port }
func (f *fakeLAN) SetPasswordHash(hash string)    { f.hash = hash }
func (f *fakeLAN) PasswordHash() string           { return f.hash }

func TestRestoreEnabledStartsListener(t *testing.T) {
	dir := t.TempDir()
	path := mobilebridge.Path(dir)
	if err := mobilebridge.Save(path, mobilebridge.State{FeatureEnabled: true, Enabled: true, Password: "secret12", LastPort: 3011}); err != nil {
		t.Fatalf("save state: %v", err)
	}
	lan := &fakeLAN{}
	bs := &bridgesvc.BridgeService{LAN: lan}
	if err := restoreMobileOnBoot(path, bs); err != nil {
		t.Fatalf("restoreMobileOnBoot: %v", err)
	}
	if !lan.started {
		t.Fatal("expected LAN listener started from persisted enabled state")
	}
	// The pairing code is never persisted: restore mints a fresh one for this
	// run. The old password only survives as a hash for the grace window.
	if lan.hash == "" || lan.hash == mobilebridge.HashPassword("secret12") {
		t.Fatalf("expected a freshly minted pairing code hash, got %q", lan.hash)
	}
	if st, _ := mobilebridge.Load(path); !sealedLegacyMatches(t, path, st.LegacyPasswordHash, "secret12") {
		t.Fatalf("legacy hash not preserved: %+v", st)
	}
	if lan.port != 3011 {
		t.Fatalf("expected persisted port reused, got %d", lan.port)
	}
}

func TestRestoreDisabledDoesNotStart(t *testing.T) {
	dir := t.TempDir()
	path := mobilebridge.Path(dir)
	if err := mobilebridge.Save(path, mobilebridge.State{Enabled: false}); err != nil {
		t.Fatalf("save state: %v", err)
	}
	lan := &fakeLAN{}
	bs := &bridgesvc.BridgeService{LAN: lan}
	if err := restoreMobileOnBoot(path, bs); err != nil {
		t.Fatalf("restoreMobileOnBoot: %v", err)
	}
	if lan.started {
		t.Fatal("expected LAN listener NOT started when persisted state is disabled")
	}
}

// TestRestoreAppliesServeToStartReturnedPort is the regression test for the
// bridge's named primary failure mode: LANManager.Start falls back to an
// ephemeral port when its persisted default is taken (observed live: asked
// for 3011, got 54014 because another Tallpine instance held 3011). A `tailscale
// serve` config pinned to the stale persisted port is exactly what secure
// pairing exists to prevent — and serve's config persists inside tailscaled
// across Tallpine restarts, so a stale entry silently proxies the tailnet at this
// machine's hostname to whatever now holds that dead port.
//
// fakeLAN.returnPort is deliberately set to diverge from the requested port
// here — do not "simplify" it back to echoing the request, or this test stops
// being able to catch a regression to the old lan.Start(state.LastPort)-then-
// applyServe(state.LastPort) bug.
func TestRestoreAppliesServeToStartReturnedPort(t *testing.T) {
	dir := t.TempDir()
	path := mobilebridge.Path(dir)
	if err := mobilebridge.Save(path, mobilebridge.State{
		FeatureEnabled: true,
		Enabled:        true,
		Password:       "secret12",
		LastPort:       3011,
		SecurePairing:  true,
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}
	lan := &fakeLAN{returnPort: 54014}
	var appliedPort int
	bs := &bridgesvc.BridgeService{
		LAN: lan,
		ApplyServe: func(port int) error {
			appliedPort = port
			return nil
		},
	}
	if err := restoreMobileOnBoot(path, bs); err != nil {
		t.Fatalf("restoreMobileOnBoot: %v", err)
	}
	if appliedPort != 54014 {
		t.Fatalf("expected ApplyServe called with Start's returned port 54014, got %d (state.LastPort was 3011)", appliedPort)
	}
}

func TestRestoreSkippedWhenFeatureFlagOff(t *testing.T) {
	dir := t.TempDir()
	path := mobilebridge.Path(dir)
	// An install from before the flag existed: bridge left enabled, flag unset.
	if err := mobilebridge.Save(path, mobilebridge.State{Enabled: true, Password: "secret12", LastPort: 3011}); err != nil {
		t.Fatalf("save state: %v", err)
	}
	lan := &fakeLAN{}
	bs := &bridgesvc.BridgeService{LAN: lan}
	if err := restoreMobileOnBoot(path, bs); err != nil {
		t.Fatalf("restoreMobileOnBoot: %v", err)
	}
	if lan.started {
		t.Fatal("mobile listener must not start at boot while the feature flag is off")
	}
}

func sealedLegacyMatches(t *testing.T, configPath, stored, pw string) bool {
	t.Helper()
	key, err := mobilebridge.LoadLegacyKey(configPath)
	if err != nil {
		t.Fatal(err)
	}
	return mobilebridge.LegacyPasswordMatches(key, stored, pw)
}
