package daemon

import (
	"context"
	"encoding/json"
	"log/slog"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/managedcfg"
	"tallpine.eu/tallpine/backend/internal/mobilebridge"
	bridgesvc "tallpine.eu/tallpine/backend/internal/mobilebridge/service"
)

func TestMobileManagedOff(t *testing.T) {
	cases := map[string]struct {
		locks map[string]json.RawMessage
		off   bool
	}{
		"no policy":      {nil, false},
		"explicit true":  {map[string]json.RawMessage{"app.mobile.enabled": json.RawMessage("true")}, false},
		"explicit false": {map[string]json.RawMessage{"app.mobile.enabled": json.RawMessage("false")}, true},
		"unusable value": {map[string]json.RawMessage{"app.mobile.enabled": nil}, true},
		"wrong type":     {map[string]json.RawMessage{"app.mobile.enabled": json.RawMessage(`"yes"`)}, true},
	}
	for name, c := range cases {
		b := &boot{managedLocks: managedcfg.New(func() map[string]json.RawMessage { return c.locks })}
		if got := b.mobileManagedOff(); got != c.off {
			t.Errorf("%s: off = %v, want %v", name, got, c.off)
		}
	}
}

// A policy pushed while the daemon runs must stop a live bridge without a restart.
func TestEnforceManagedMobileStopsRunningBridgeLive(t *testing.T) {
	path := mobilebridge.Path(t.TempDir())
	if err := mobilebridge.Save(path, mobilebridge.State{FeatureEnabled: true, Enabled: true, LastPort: 3011}); err != nil {
		t.Fatal(err)
	}
	var off atomic.Bool
	var polls atomic.Int32
	disabled := make(chan struct{}, 1)
	bs := &bridgesvc.BridgeService{
		LAN:        &fakeLAN{},
		ConfigPath: path,
		ManagedOff: func() bool { polls.Add(1); return off.Load() },
		OnBrowserRemoteAccessDisabled: func() {
			select {
			case disabled <- struct{}{}:
			default:
			}
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go enforceManagedMobile(ctx, time.Millisecond, bs, slog.Default())

	// Wait for the loop to enforce a few times under an allowing policy; no
	// wall-clock sleep, so a slow machine cannot skip the "allowed" check.
	for polls.Load() < 3 {
		runtime.Gosched()
	}
	if st, _ := mobilebridge.Load(path); !st.FeatureEnabled {
		t.Fatal("bridge torn down while the policy allows mobile")
	}
	off.Store(true)
	select {
	case <-disabled:
	case <-time.After(30 * time.Second):
		t.Fatal("bridge still enabled after the policy switched mobile off")
	}
	if st, _ := mobilebridge.Load(path); st.FeatureEnabled || st.Enabled {
		t.Fatalf("bridge state after managed-off enforcement = %+v, want disabled", st)
	}
}
