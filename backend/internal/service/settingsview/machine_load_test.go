package settingsview

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/service/machineload"
)

// The machine-load variables force a limit (and lock the setting) only when
// they hold a whole number inside the setting's range.
func TestMachineLoadEnvOverridesAndLocks(t *testing.T) {
	cases := []struct {
		env    string
		value  string
		source string
		locked bool
	}{
		{"5", "5", SourceEnv, true},
		{"0", "0", SourceEnv, true},
		{" 7 ", "7", SourceEnv, true},
		{"101", "3", SourceDefault, false},
		{"-1", "3", SourceDefault, false},
		{"many", "3", SourceDefault, false},
		{"", "3", SourceDefault, false},
	}
	for _, c := range cases {
		snap := freshSnapshot()
		snap.MachineMaxReviewers, snap.MachineLoadPercent = machineload.DefaultMaxReviewers, machineload.DefaultLoadPercent
		svc := &Service{AppSettings: fakeApp{snap},
			LookupEnv: func(name string) (string, bool) { return c.env, name == machineload.EnvMaxReviewers }}
		values, err := svc.App(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		v := byKey(values)["app.machineLoad.maxReviewers"]
		if string(v.Value) != c.value || v.Source != c.source || v.Locked != c.locked {
			t.Errorf("env %q: %+v", c.env, v)
		}
	}
	snap := freshSnapshot()
	snap.MachineMaxReviewers, snap.MachineLoadPercent = machineload.DefaultMaxReviewers, machineload.DefaultLoadPercent
	svc := &Service{AppSettings: fakeApp{snap}, LookupEnv: func(name string) (string, bool) { return "250", name == machineload.EnvLoadPercent }}
	values, _ := svc.App(context.Background())
	if v := byKey(values)["app.machineLoad.loadPercent"]; string(v.Value) != "250" || !v.Locked || v.LockedBy != machineload.EnvLoadPercent {
		t.Errorf("loadPercent = %+v", v)
	}
}

// With no overrides the registry reports the shipped defaults.
func TestMachineLoadDefaultsInCatalog(t *testing.T) {
	snap := freshSnapshot()
	snap.MachineMaxReviewers, snap.MachineLoadPercent = machineload.DefaultMaxReviewers, machineload.DefaultLoadPercent
	values, err := (&Service{AppSettings: fakeApp{snap}}).App(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := byKey(values)
	if v := got["app.machineLoad.maxReviewers"]; string(v.Value) != "3" || v.Source != SourceDefault {
		t.Errorf("maxReviewers = %+v, want default 3", v)
	}
	if v := got["app.machineLoad.loadPercent"]; string(v.Value) != "100" || v.Source != SourceDefault {
		t.Errorf("loadPercent = %+v, want default 100", v)
	}
}
