package httpd

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/mobilebridge"
	bridgesvc "tallpine.eu/tallpine/backend/internal/mobilebridge/service"
)

// A phone paired the old way holds the 8-char password as its bearer. After the
// daemon upgrades its config and restarts, that phone must keep working for the
// 14-day grace window and then be told to re-pair, without the daemon ever
// keeping the plaintext.
func TestOldPairedPhoneWorksThroughGraceWindowThenMustRepair(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "mobile.json")
	if err := os.WriteFile(configPath, []byte(`{"featureEnabled":true,"enabled":true,"password":"Ab3dEf9h","lastPort":0}`), 0o600); err != nil {
		t.Fatal(err)
	}
	upgradedAt := time.Now()
	state, err := mobilebridge.Load(configPath) // the upgrade: first Load after this release
	if err != nil {
		t.Fatal(err)
	}
	if state.LegacyUntil.Before(upgradedAt.Add(mobilebridge.LegacyGrace)) {
		t.Fatalf("grace window ends %v, want at least %v after the upgrade", state.LegacyUntil, mobilebridge.LegacyGrace)
	}

	var mu sync.Mutex
	now := upgradedAt
	if !state.RequireDevicePairing {
		t.Fatal("default state must require device pairing")
	}
	m := NewMobileLAN(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }), "", 0, nil)
	m.state.now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	t.Cleanup(func() { _ = m.Stop(t.Context()) })
	if err := (&bridgesvc.BridgeService{LAN: m, ConfigPath: configPath}).RestoreOnBoot(state); err != nil { // daemon restart
		t.Fatal(err)
	}
	at := func(d time.Duration) { mu.Lock(); now = upgradedAt.Add(d); mu.Unlock() }
	call := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		m.handler.ServeHTTP(w, req("Bearer Ab3dEf9h"))
		return w
	}

	for _, offset := range []time.Duration{time.Minute, 7 * 24 * time.Hour, mobilebridge.LegacyGrace - time.Minute} {
		at(offset)
		if w := call(); w.Code != http.StatusOK {
			t.Fatalf("old bearer %v after the upgrade = %d, want 200", offset, w.Code)
		}
	}
	at(mobilebridge.LegacyGrace + time.Minute)
	w := call()
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "LEGACY_PAIRING_EXPIRED") {
		t.Fatalf("old bearer after the window = %d %s, want 401 LEGACY_PAIRING_EXPIRED", w.Code, w.Body)
	}
}

// With per-device pairing off (the user opted out; the LAN manager zero value) a legacy pairing never expires,
// so the App Store build that cannot do the token exchange keeps working. The
// shell routes stay blocked on plain LAN for it, as for every pairing.
func TestLegacyPairingNeverExpiresByDefault(t *testing.T) {
	m, _ := newPairingManager(t, "")
	now := time.Now()
	m.state.now = func() time.Time { return now.Add(10 * 365 * 24 * time.Hour) }
	m.SetLegacyPassword(mobilebridge.HashPassword("Ab3dEf9h"), now.Add(-time.Hour)) // window long over
	for _, path := range []string{"/api/v1/sessions", "/api/v1/sessions/tp-1"} {
		w := httptest.NewRecorder()
		m.dispatch().ServeHTTP(w, shellRequest("192.168.1.20:5000", path, "Ab3dEf9h"))
		if w.Code != http.StatusOK {
			t.Errorf("legacy bearer %s past the window = %d %s, want 200", path, w.Code, w.Body)
		}
	}
}

// Opting in to per-device pairing makes the same state expire, live (no restart).
func TestRequireDevicePairingEnforcesLegacyExpiry(t *testing.T) {
	m, _ := newPairingManager(t, "")
	now := time.Now()
	m.state.now = func() time.Time { return now }
	m.SetLegacyPassword(mobilebridge.HashPassword("Ab3dEf9h"), now.Add(-time.Hour))
	call := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		m.dispatch().ServeHTTP(w, shellRequest("192.168.1.20:5000", "/api/v1/sessions", "Ab3dEf9h"))
		return w
	}
	if w := call(); w.Code != http.StatusOK {
		t.Fatalf("default = %d, want 200", w.Code)
	}
	m.SetRequireDevicePairing(true)
	if w := call(); w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "LEGACY_PAIRING_EXPIRED") {
		t.Fatalf("opted in = %d %s, want 401 LEGACY_PAIRING_EXPIRED", w.Code, w.Body)
	}
	m.SetRequireDevicePairing(false)
	if w := call(); w.Code != http.StatusOK {
		t.Fatalf("opted back out = %d, want 200", w.Code)
	}
}

// A daemon restarted after the grace window already passed must still answer an
// old bearer with LEGACY_PAIRING_EXPIRED (not BAD_PASSWORD plus a lockout
// failure), so the phone knows to re-pair.
func TestLegacyBearerAfterRestartPastGraceGetsPairingExpired(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "mobile.json")
	if err := os.WriteFile(configPath, []byte(`{"featureEnabled":true,"enabled":true,"password":"Ab3dEf9h","lastPort":0}`), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := mobilebridge.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	state.LegacyUntil = time.Now().Add(-time.Hour)
	if err := mobilebridge.Save(configPath, state); err != nil {
		t.Fatal(err)
	}
	state, err = mobilebridge.Load(configPath) // restart after the window passed
	if err != nil {
		t.Fatal(err)
	}
	m := NewMobileLAN(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }), "", 0, nil)
	t.Cleanup(func() { _ = m.Stop(t.Context()) })
	if err := (&bridgesvc.BridgeService{LAN: m, ConfigPath: configPath}).RestoreOnBoot(state); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	m.handler.ServeHTTP(w, req("Bearer Ab3dEf9h"))
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "LEGACY_PAIRING_EXPIRED") {
		t.Fatalf("got %d %s, want 401 LEGACY_PAIRING_EXPIRED", w.Code, w.Body)
	}
}
