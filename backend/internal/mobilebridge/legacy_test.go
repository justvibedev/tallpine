package mobilebridge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadMigratesPlaintextPasswordInPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"enabled":true,"password":"Abcd1234","lastPort":3011}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Password != "" || !legacyMatches(t, path, s.LegacyPasswordHash, "Abcd1234") {
		t.Fatalf("not migrated: %+v", s)
	}
	if until := time.Until(s.LegacyUntil); until < LegacyGrace-time.Minute || until > LegacyGrace+time.Minute {
		t.Fatalf("grace window = %v, want about %v", until, LegacyGrace)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "Abcd1234") {
		t.Fatalf("plaintext left on disk: %s", raw)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
	// A second Load must not restart the window.
	again, _ := Load(path)
	if !again.LegacyUntil.Equal(s.LegacyUntil) {
		t.Fatal("grace window restarted on reload")
	}
}

func TestLoadTightensExistingFileMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"enabled":false}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
}

func legacyMatches(t *testing.T, configPath, stored, pw string) bool {
	t.Helper()
	key, err := LoadLegacyKey(configPath)
	if err != nil {
		t.Fatal(err)
	}
	return LegacyPasswordMatches(key, stored, pw)
}
