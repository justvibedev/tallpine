package awake

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestLowBatteryRecoversWithFreshSafeReadings(t *testing.T) {
	for _, test := range []struct {
		name                       string
		percent                    int
		onBattery                  bool
		thermal                    string
		unknown, stale, wantActive bool
	}{
		{name: "charged", percent: 87, onBattery: true, thermal: "nominal", wantActive: true},
		{name: "plugged in", percent: 9, thermal: "nominal", wantActive: true},
		{name: "still low", percent: 9, onBattery: true, thermal: "nominal"},
		{name: "at threshold", percent: 10, onBattery: true, thermal: "nominal"},
		{name: "unknown battery", percent: 87, onBattery: true, thermal: "nominal", unknown: true},
		{name: "unknown heat", percent: 87, onBattery: true, thermal: "unknown"},
		{name: "critical heat", percent: 87, onBattery: true, thermal: "critical"},
		{name: "stale", percent: 87, onBattery: true, thermal: "nominal", stale: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, store, h, _, now := fixture(t)
			start(t, s, domain.StartAwakeRequest{Mode: "indefinite", LidClosed: true})
			low, battery := 9, true
			h.observation.BatteryPercent, h.observation.OnBattery = &low, &battery
			tick(t, s)
			if h.active || s.Status().Reason != "low_battery" || !s.needsObservations() {
				t.Fatal("battery stop must release wake and keep observing")
			}
			h.reason = "low_battery" // The helper can retain the previous stop reason.
			h.observation.BatteryPercent, h.observation.OnBattery = &test.percent, &test.onBattery
			h.observation.Thermal = test.thermal
			if test.unknown {
				h.observation.BatteryPercent = nil
			}
			s.refresh(context.Background())
			if test.stale {
				*now = now.Add(20 * time.Second)
			}
			if err := s.reconcile(context.Background()); err != nil {
				t.Fatal(err)
			}
			if h.active != test.wantActive {
				t.Fatalf("active=%v, want %v", h.active, test.wantActive)
			}
			if test.wantActive {
				var persisted domain.AwakeDocument
				if err := json.Unmarshal([]byte(store.document), &persisted); err != nil {
					t.Fatal(err)
				}
				if persisted.Paused || persisted.StopReason != "" || !h.lid {
					t.Fatal("recovery did not restore and persist existing intent")
				}
			}
		})
	}
}

func TestFreshBatteryReadingStillReleasesNativeBlockedLease(t *testing.T) {
	s, _, h, _, _ := fixture(t)
	start(t, s, domain.StartAwakeRequest{Mode: "indefinite"})
	// The helper can report its stop after the battery has already recovered.
	h.reason = "low_battery"
	tick(t, s)
	if h.active || s.Status().Reason != "low_battery" {
		t.Fatal("native stop did not release the blocked lease")
	}
	tick(t, s)
	if !h.active || s.Status().Paused {
		t.Fatal("fresh safe readings did not restart with new ownership")
	}
}

func TestBatteryRecoveryPreservesPreviouslyPausedRules(t *testing.T) {
	s, _, h, _, _ := fixture(t)
	if err := s.StopAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	start(t, s, domain.StartAwakeRequest{Mode: "indefinite"})
	low, battery := 9, true
	h.observation.BatteryPercent, h.observation.OnBattery = &low, &battery
	tick(t, s)
	charged := 87
	h.observation.BatteryPercent = &charged
	tick(t, s)
	if !h.active || !s.Status().Paused {
		t.Fatal("manual battery recovery must preserve the earlier pause of automatic rules")
	}
}

func TestTurningOffBatteryStoppedRulesCancelsRecovery(t *testing.T) {
	s, _, h, bindings, now := fixture(t)
	bindings.agent = true
	rule := domain.AwakeRule{ID: "work", Name: "Agent work", Enabled: true, Match: "all", Conditions: []domain.AwakeCondition{{Kind: "agent_activity"}}}
	if err := s.UpdateRules(context.Background(), []domain.AwakeRule{rule}); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(5 * time.Second)
	tick(t, s)
	low, battery := 9, true
	h.observation.BatteryPercent, h.observation.OnBattery = &low, &battery
	tick(t, s)
	if err := s.StopAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	charged := 87
	h.observation.BatteryPercent = &charged
	tick(t, s)
	if h.active || !s.Status().Paused || s.needsObservations() {
		t.Fatal("battery recovery undid the user's explicit off action")
	}
}

func TestBatteryRecoveryPersistsBeforeRestoringWake(t *testing.T) {
	s, store, h, _, _ := fixture(t)
	start(t, s, domain.StartAwakeRequest{Mode: "indefinite"})
	low, battery := 9, true
	h.observation.BatteryPercent, h.observation.OnBattery = &low, &battery
	tick(t, s)
	charged := 87
	h.observation.BatteryPercent = &charged
	s.refresh(context.Background())
	store.fail = true
	if err := s.reconcile(context.Background()); err == nil || h.active {
		t.Fatal("failed recovery persistence must not restore wake")
	}
	store.fail = false
	tick(t, s)
	if !h.active {
		t.Fatal("durable retry did not restore wake")
	}
}

func TestBatteryRecoveryDoesNotReviveEndedRequests(t *testing.T) {
	for _, end := range []string{"off", "expired", "session ended"} {
		t.Run(end, func(t *testing.T) {
			s, _, h, bindings, now := fixture(t)
			req := domain.StartAwakeRequest{Mode: "duration", DurationSeconds: 30}
			if end == "session ended" {
				req = domain.StartAwakeRequest{Scope: "session", SessionID: "s1", Mode: "indefinite"}
			}
			start(t, s, req)
			low, battery := 9, true
			h.observation.BatteryPercent, h.observation.OnBattery = &low, &battery
			tick(t, s)
			switch end {
			case "off":
				if err := s.StopAll(context.Background()); err != nil {
					t.Fatal(err)
				}
			case "expired":
				*now = now.Add(time.Minute)
			case "session ended":
				bindings.alive = false
			}
			charged := 87
			h.observation.BatteryPercent = &charged
			tick(t, s)
			if h.active {
				t.Fatal("battery recovery revived an ended request")
			}
			if end == "off" && !s.Status().Paused {
				t.Fatal("battery recovery undid explicit pause")
			}
		})
	}
}
