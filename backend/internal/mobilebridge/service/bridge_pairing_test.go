package service

import (
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/mobilebridge"
)

type credLAN struct {
	fakeLAN
	hashMu     sync.Mutex
	legacyHash string
	requireDev bool
	hook       func()
}

// SetPasswordHash is slowed so that two arms that are not serialized by the
// caller visibly interleave.
func (c *credLAN) SetPasswordHash(h string) {
	c.hashMu.Lock()
	c.hash = h
	c.hashMu.Unlock()
	time.Sleep(time.Duration(rand.IntN(100)) * time.Microsecond)
}
func (c *credLAN) PasswordHash() string {
	c.hashMu.Lock()
	defer c.hashMu.Unlock()
	return c.hash
}
func (c *credLAN) SetLegacyPassword(h string, _ time.Time) { c.legacyHash = h }
func (c *credLAN) SetLegacyKey([]byte)                     {}
func (c *credLAN) SetRequireDevicePairing(on bool)         { c.requireDev = on }
func (c *credLAN) SetPairedHook(fn func())                 { c.hook = fn }

func enabledBridge(t *testing.T, seed mobilebridge.State) (*BridgeService, *credLAN) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mobile", "config.json")
	seed.FeatureEnabled = true
	if err := mobilebridge.Save(path, seed); err != nil {
		t.Fatal(err)
	}
	lan := &credLAN{}
	return &BridgeService{LAN: lan, ConfigPath: path, DefaultPort: 3011}, lan
}

func TestPairingCodeIsShownButNeverPersisted(t *testing.T) {
	b, lan := enabledBridge(t, mobilebridge.State{})
	st, err := b.Enable()
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Password) != 8 {
		t.Fatalf("status shows no pairing code: %q", st.Password)
	}
	state, _ := mobilebridge.Load(b.ConfigPath)
	if state.LegacyPasswordHash != "" {
		t.Fatal("a fresh enable must not persist any credential derived from the pairing code")
	}
	raw, _ := os.ReadFile(b.ConfigPath)
	if strings.Contains(string(raw), st.Password) {
		t.Fatalf("pairing code persisted: %s", raw)
	}
	if lan.hash != mobilebridge.HashPassword(st.Password) {
		t.Fatal("listener not armed with the displayed code's hash")
	}
}

func TestEnableCarriesLegacySettingsToTheListener(t *testing.T) {
	b, lan := enabledBridge(t, mobilebridge.State{LegacyPasswordHash: "hmac1:legacyhash", LegacyUntil: time.Now().Add(time.Hour)})
	if _, err := b.Enable(); err != nil {
		t.Fatal(err)
	}
	if lan.legacyHash != "hmac1:legacyhash" || lan.hook == nil {
		t.Fatalf("listener not armed: %+v", lan)
	}
	state, _ := mobilebridge.Load(b.ConfigPath)
	if state.LegacyPasswordHash != "hmac1:legacyhash" {
		t.Fatalf("Enable dropped persisted settings: %+v", state)
	}
}

func TestRedeemedCodeIsReplacedWithAFreshOne(t *testing.T) {
	b, lan := enabledBridge(t, mobilebridge.State{})
	st, _ := b.Enable()
	lan.hook() // the listener redeemed the code
	next := b.Status().Password
	if next == "" || next == st.Password {
		t.Fatalf("code after pairing = %q (was %q), want a fresh one", next, st.Password)
	}
	if lan.hash != mobilebridge.HashPassword(next) {
		t.Fatal("fresh code not armed on the listener")
	}
}

func TestExpiredCodeIsReplacedOnStatus(t *testing.T) {
	b, _ := enabledBridge(t, mobilebridge.State{})
	st, _ := b.Enable()
	b.pairing.mu.Lock()
	b.pairing.minted = time.Now().Add(-pairingCodeTTL - time.Minute)
	b.pairing.mu.Unlock()
	if next := b.Status().Password; next == "" || next == st.Password {
		t.Fatalf("expired code still shown: %q", next)
	}
}

func TestRegenerateEndsLegacyGraceWindowWhenDevicePairingRequired(t *testing.T) {
	b, lan := enabledBridge(t, mobilebridge.State{LegacyPasswordHash: "hmac1:legacyhash", LegacyUntil: time.Now().Add(time.Hour), RequireDevicePairing: true})
	if _, err := b.Enable(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Regenerate(); err != nil {
		t.Fatal(err)
	}
	state, _ := mobilebridge.Load(b.ConfigPath)
	if state.LegacyPasswordHash != "hmac1:legacyhash" || lan.legacyHash != "hmac1:legacyhash" {
		t.Fatalf("legacy hash must be kept so the phone sees LEGACY_PAIRING_EXPIRED: persisted=%q armed=%q", state.LegacyPasswordHash, lan.legacyHash)
	}
	if state.LegacyUntil.After(time.Now()) || state.LegacyUntil.IsZero() {
		t.Fatalf("grace window still open after Regenerate: %v", state.LegacyUntil)
	}
}

// With the switch off, "New code" (e.g. to pair a second phone) must not lock
// out the older app that is only reachable through the legacy credential.
func TestRegenerateKeepsLegacyPairingByDefault(t *testing.T) {
	b, lan := enabledBridge(t, mobilebridge.State{LegacyPasswordHash: "hmac1:legacyhash", LegacyUntil: time.Now().Add(time.Hour)})
	if _, err := b.Enable(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Regenerate(); err != nil {
		t.Fatal(err)
	}
	state, _ := mobilebridge.Load(b.ConfigPath)
	if state.LegacyPasswordHash != "hmac1:legacyhash" || lan.legacyHash != "hmac1:legacyhash" {
		t.Fatalf("legacy credential lost on Regenerate: persisted=%q armed=%q", state.LegacyPasswordHash, lan.legacyHash)
	}
}

// Status, the paired hook and Regenerate all re-arm the code; whichever runs
// last must leave the listener armed with the code that is shown.
func TestShownPairingCodeAndArmedHashNeverDrift(t *testing.T) {
	b, lan := enabledBridge(t, mobilebridge.State{})
	if _, err := b.Enable(); err != nil {
		t.Fatal(err)
	}
	for round := range 200 {
		var wg sync.WaitGroup
		for _, arm := range []func(){b.OnPaired, func() { _ = b.mintPairingCode() }, b.OnPaired} {
			wg.Add(1)
			go func() { defer wg.Done(); arm() }()
		}
		wg.Wait()
		if shown := b.shownPairingCode(); lan.PasswordHash() != mobilebridge.HashPassword(shown) {
			t.Fatalf("round %d: armed hash %q does not match the shown code %q", round, lan.PasswordHash(), shown)
		}
	}
}

func TestRequireDevicePairingPersistsAndReachesTheListener(t *testing.T) {
	b, lan := enabledBridge(t, mobilebridge.State{LegacyPasswordHash: "hmac1:legacyhash", LegacyUntil: time.Now().Add(time.Hour)})
	if _, err := b.Enable(); err != nil {
		t.Fatal(err)
	}
	if lan.requireDev {
		t.Fatal("per-device pairing must be off by default")
	}
	res, err := b.SetRequireDevicePairing(true)
	if err != nil || !res.RequireDevicePairing || !lan.requireDev {
		t.Fatalf("set on: err=%v status=%v listener=%v", err, res.RequireDevicePairing, lan.requireDev)
	}
	if st, _ := mobilebridge.Load(b.ConfigPath); !st.RequireDevicePairing {
		t.Fatal("setting not persisted")
	}
	// A later Enable (restart, re-enable) re-arms the persisted choice.
	lan.requireDev = false
	if _, err := b.Enable(); err != nil {
		t.Fatal(err)
	}
	if !lan.requireDev {
		t.Fatal("Enable did not re-arm RequireDevicePairing on the listener")
	}
}

// Turning pairing off after Regenerate must not bring the retired legacy
// password back.
func TestTurningDevicePairingOffDoesNotResurrectExpiredLegacyPassword(t *testing.T) {
	b, lan := enabledBridge(t, mobilebridge.State{LegacyPasswordHash: "hmac1:legacyhash", LegacyUntil: time.Now().Add(time.Hour), RequireDevicePairing: true})
	if _, err := b.Enable(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Regenerate(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SetRequireDevicePairing(false); err != nil {
		t.Fatal(err)
	}
	state, _ := mobilebridge.Load(b.ConfigPath)
	if state.LegacyPasswordAccepted(time.Now()) || state.LegacyPasswordHash != "" || lan.legacyHash != "" {
		t.Fatalf("expired legacy password resurrected: persisted=%q armed=%q", state.LegacyPasswordHash, lan.legacyHash)
	}
}

// A grace window that merely ended (no Regenerate) is not a revocation: turning
// the setting off lets the older app keep working, as the Settings copy says.
func TestTurningDevicePairingOffRestoresLegacyAfterOrdinaryExpiry(t *testing.T) {
	b, lan := enabledBridge(t, mobilebridge.State{LegacyPasswordHash: "hmac1:legacyhash", LegacyUntil: time.Now().Add(-time.Hour), RequireDevicePairing: true})
	if _, err := b.Enable(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SetRequireDevicePairing(false); err != nil {
		t.Fatal(err)
	}
	state, _ := mobilebridge.Load(b.ConfigPath)
	if !state.LegacyPasswordAccepted(time.Now()) || lan.legacyHash != "hmac1:legacyhash" {
		t.Fatalf("legacy pairing dropped on ordinary expiry: persisted=%q armed=%q", state.LegacyPasswordHash, lan.legacyHash)
	}
}
