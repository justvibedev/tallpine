package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/brand"
)

// A packaged-path run (no data dir override) with a legacy DB and no new home
// must refuse to start, and Run must surface that before touching any state.
func TestRunRefusesLegacyInstallWithoutNewHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TALLPINE_DATA_DIR", "")
	os.Unsetenv("TALLPINE_DATA_DIR")
	db := filepath.Join(home, brand.LegacyHomeDirName, "data", "ao.db")
	if err := os.MkdirAll(filepath.Dir(db), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(db, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- Run() }()
	var err error
	select {
	case err = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run() did not refuse to start; the guard did not fire")
	}
	if err == nil || !strings.Contains(err.Error(), "migrate-from-vo") {
		t.Fatalf("Run() = %v, want refusal pointing at migrate-from-vo", err)
	}
	if _, statErr := os.Stat(filepath.Join(home, brand.NextHomeDirName)); statErr == nil {
		t.Fatal("refused start must not create the new home")
	}
}

func TestGuardLegacyInstallHonoursDataDirOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TALLPINE_DATA_DIR", filepath.Join(home, "scratch"))
	db := filepath.Join(home, brand.LegacyHomeDirName, "data", "ao.db")
	_ = os.MkdirAll(filepath.Dir(db), 0o700)
	_ = os.WriteFile(db, []byte("x"), 0o600)
	if err := guardLegacyInstall(); err != nil {
		t.Fatalf("explicit data dir must bypass the guard, got %v", err)
	}
}
