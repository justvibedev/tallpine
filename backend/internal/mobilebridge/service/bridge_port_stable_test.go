package service

import (
	"path/filepath"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/mobilebridge"
)

// A disable/enable cycle must come back on the port phones were paired against,
// not drift to the default (which may be busy) and persist a new one.
func TestEnableReusesPersistedGatewayPort(t *testing.T) {
	b := &BridgeService{
		LAN:         &fakeLAN{},
		ConfigPath:  filepath.Join(t.TempDir(), "mobile", "config.json"),
		DefaultPort: 3011,
	}
	if err := mobilebridge.Save(b.ConfigPath, mobilebridge.State{LastPort: 54321}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Enable(); err != nil {
		t.Fatal(err)
	}
	st, _ := mobilebridge.Load(b.ConfigPath)
	if st.LastPort != 54321 {
		t.Fatalf("LastPort = %d, want the persisted 54321", st.LastPort)
	}
}

func TestStatusReportsLegacyUntil(t *testing.T) {
	b := &BridgeService{LAN: &fakeLAN{}, ConfigPath: filepath.Join(t.TempDir(), "mobile", "config.json"), DefaultPort: 3011}
	until := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	if err := mobilebridge.Save(b.ConfigPath, mobilebridge.State{LegacyPasswordHash: "h", LegacyUntil: until}); err != nil {
		t.Fatal(err)
	}
	if got := b.Status().LegacyUntil; got != "2026-11-13T10:00:00Z" {
		t.Fatalf("LegacyUntil = %q", got)
	}
}
