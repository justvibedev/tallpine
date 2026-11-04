package mobilebridge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadSealsLegacyHashUnderPerInstallKey(t *testing.T) {
	path := Path(t.TempDir())
	plain := HashPassword("Abcd1234")
	if err := Save(path, State{Enabled: true, LegacyPasswordHash: plain, LegacyUntil: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !IsSealedLegacyHash(s.LegacyPasswordHash) || s.LegacyPasswordHash == plain {
		t.Fatalf("hash not sealed: %q", s.LegacyPasswordHash)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), plain) {
		t.Fatalf("unkeyed hash still on disk: %s", raw)
	}
	keyPath := filepath.Join(filepath.Dir(path), legacyKeyFile)
	if info, err := os.Stat(keyPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key file: %v %v", info, err)
	}
	key, _ := LoadLegacyKey(path)
	if !LegacyPasswordMatches(key, s.LegacyPasswordHash, "Abcd1234") || LegacyPasswordMatches(key, s.LegacyPasswordHash, "Abcd1235") {
		t.Fatal("sealed hash must match the password and nothing else")
	}
	// The copied config alone (another install's key) cannot verify a guess.
	other := make([]byte, 32)
	if LegacyPasswordMatches(other, s.LegacyPasswordHash, "Abcd1234") || LegacyPasswordMatches(nil, s.LegacyPasswordHash, "Abcd1234") {
		t.Fatal("sealed hash verified without the install key")
	}
	again, _ := Load(path)
	if again.LegacyPasswordHash != s.LegacyPasswordHash {
		t.Fatal("sealing must be stable across loads")
	}
}

func TestLoadKeepsExpiredLegacyHashSoPhoneSeesLegacyPairingExpired(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name    string
		require bool
		until   time.Time
		wiped   bool
	}{
		{"binding and over", true, now.Add(-time.Minute), false},
		{"binding but open", true, now.Add(time.Hour), false},
		{"not binding, still accepted", false, now.Add(-time.Minute), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := Path(t.TempDir())
			if err := Save(path, State{LegacyPasswordHash: HashPassword("x"), LegacyUntil: c.until, RequireDevicePairing: c.require}); err != nil {
				t.Fatal(err)
			}
			s, _ := Load(path)
			if (s.LegacyPasswordHash == "") != c.wiped {
				t.Fatalf("hash after load = %q, wiped want %v", s.LegacyPasswordHash, c.wiped)
			}
			if want := !c.require || !c.until.Before(now); s.LegacyPasswordAccepted(now) != want {
				t.Fatalf("LegacyPasswordAccepted = %v, want %v", s.LegacyPasswordAccepted(now), want)
			}
		})
	}
}
