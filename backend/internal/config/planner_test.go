package config

import "testing"

func TestPlannerSwitch_DefaultOn(t *testing.T) {
	t.Setenv("TALLPINE_PLANNER", "")
	cfg, err := Load()
	if err != nil || !cfg.Planner {
		t.Fatalf("Load() = planner %v, %v; want on by default", cfg.Planner, err)
	}
	for _, raw := range []string{"off", "0", "false"} {
		t.Setenv("TALLPINE_PLANNER", raw)
		cfg, err := Load()
		if err != nil || cfg.Planner {
			t.Errorf("Load(%q) = %v, %v; want planner off", raw, cfg.Planner, err)
		}
	}
}
