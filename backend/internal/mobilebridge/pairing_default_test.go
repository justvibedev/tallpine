package mobilebridge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFreshStateRequiresDevicePairing(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "mobile", "config.json"))
	if err != nil || !s.RequireDevicePairing {
		t.Fatalf("fresh: %+v err %v", s, err)
	}
}

func TestExplicitOffSurvivesSaveLoad(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	if err := Save(p, State{Enabled: true, RequireDevicePairing: false}); err != nil {
		t.Fatal(err)
	}
	s, err := Load(p)
	if err != nil || s.RequireDevicePairing {
		t.Fatalf("explicit off lost: %+v err %v", s, err)
	}
}

func TestFileWithoutKeyBecomesOn(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(`{"enabled":true,"lastPort":3011}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, _ := Load(p)
	if !s.RequireDevicePairing {
		t.Fatal("missing key should become on")
	}
	raw, _ := os.ReadFile(p)
	if !strings.Contains(string(raw), `"requireDevicePairing": true`) {
		t.Fatalf("key not persisted: %s", raw)
	}
}

func TestLegacyPasswordRejectedAfterGraceByDefault(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	if err := os.WriteFile(p, []byte(`{"enabled":true,"legacyPasswordHash":"x","legacyUntil":"`+past+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, _ := Load(p)
	if s.LegacyPasswordAccepted(time.Now()) {
		t.Fatal("legacy password accepted after grace")
	}
}

// A caller that ignores Load's error and Saves the result must not turn an
// unreadable file into an explicit "off".
func TestLoadErrorKeepsSecureDefault(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(`{not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(p)
	if err == nil || !s.RequireDevicePairing {
		t.Fatalf("parse error: state=%+v err=%v", s, err)
	}
	dir := t.TempDir()
	s, err = Load(dir) // reading a directory fails
	if err == nil || !s.RequireDevicePairing {
		t.Fatalf("read error: state=%+v err=%v", s, err)
	}
}
