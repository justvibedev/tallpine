package service

import (
	"errors"
	"testing"

	"tallpine.eu/tallpine/backend/internal/mobilebridge"
)

func TestManagedOffRefusesEveryEnablePath(t *testing.T) {
	b := featureBridge(t, nil)
	b.ManagedOff = func() bool { return true }
	if _, err := b.SetFeatureEnabled(true); !errors.Is(err, ErrManagedOff) {
		t.Fatalf("SetFeatureEnabled err = %v", err)
	}
	for name, fn := range map[string]func() error{
		"Enable":     func() error { _, err := b.Enable(); return err },
		"LANOnly":    func() error { _, err := b.EnableLANOnly(); return err },
		"TunnelOnly": func() error { _, err := b.EnableTunnelOnly(); return err },
	} {
		if err := fn(); !errors.Is(err, ErrManagedOff) {
			t.Fatalf("%s err = %v", name, err)
		}
	}
	if b.LAN.Running() {
		t.Fatal("listener started under managed policy")
	}
}

func TestManagedOffBlocksRestoreAndEnforcesOff(t *testing.T) {
	b := featureBridge(t, nil)
	if _, err := b.SetFeatureEnabled(true); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Enable(); err != nil {
		t.Fatal(err)
	}
	b.ManagedOff = func() bool { return true }
	if got := b.Status(); got.FeatureEnabled {
		t.Fatalf("status reports feature on under policy: %+v", got)
	}
	if err := b.EnforceManagedOff(); err != nil {
		t.Fatal(err)
	}
	st, _ := mobilebridge.Load(b.ConfigPath)
	if st.FeatureEnabled || st.Enabled || b.LAN.Running() {
		t.Fatalf("not torn down: %+v running=%v", st, b.LAN.Running())
	}
	st.FeatureEnabled, st.Enabled = true, true
	if err := b.RestoreOnBoot(st); err != nil || b.LAN.Running() {
		t.Fatalf("restore under policy: err=%v running=%v", err, b.LAN.Running())
	}
}
