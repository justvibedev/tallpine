package config

import "testing"

func TestLoadBacklogRunIsOnByDefault(t *testing.T) {
	for _, raw := range []string{"", " "} {
		t.Setenv("TALLPINE_BACKLOG_RUN", raw)
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load(%q): %v", raw, err)
		}
		if !cfg.BacklogRun {
			t.Errorf("BacklogRun = false for %q, want on by default", raw)
		}
	}
}

func TestLoadBacklogRunSwitchTurnsOffAndRejectsGarbage(t *testing.T) {
	for _, raw := range []string{"off", "0", "false", "no"} {
		t.Setenv("TALLPINE_BACKLOG_RUN", raw)
		cfg, err := Load()
		if err != nil || cfg.BacklogRun {
			t.Errorf("Load(%q) = %v, %v; want BacklogRun off", raw, cfg.BacklogRun, err)
		}
	}
	for _, raw := range []string{"on", "true", "1", "yes"} {
		t.Setenv("TALLPINE_BACKLOG_RUN", raw)
		cfg, err := Load()
		if err != nil || !cfg.BacklogRun {
			t.Errorf("Load(%q) = %v, %v; want BacklogRun on", raw, cfg.BacklogRun, err)
		}
	}
	t.Setenv("TALLPINE_BACKLOG_RUN", "maybe")
	if _, err := Load(); err == nil {
		t.Fatal("malformed TALLPINE_BACKLOG_RUN accepted")
	}
}
