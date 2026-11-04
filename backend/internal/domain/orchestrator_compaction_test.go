package domain

import "testing"

func TestDefaultAutoCompactChoice(t *testing.T) {
	t.Parallel()
	orch := func(w string) ProjectConfig { return ProjectConfig{Orchestrator: RoleOverride{AutoCompactWindow: w}} }
	for _, tc := range []struct {
		name      string
		kind      SessionKind
		project   ProjectConfig
		harness   string
		envWindow bool
		want      string
	}{
		{"worker keeps Auto", KindWorker, ProjectConfig{}, "", false, ""},
		{"worker keeps the Settings default", KindWorker, orch("1m"), "60%", false, "60%"},
		{"orchestrator gets the built-in window", KindOrchestrator, ProjectConfig{}, "", false, "120k"},
		// B398: a worker-sized Settings default (40% = 400k on a 1M model) must
		// not shadow the orchestrator default.
		{"built-in wins over Settings default", KindOrchestrator, ProjectConfig{}, "40%", false, "120k"},
		{"project env wins over Settings default", KindOrchestrator, ProjectConfig{}, "40%", true, ""},
		{"project window wins over Settings", KindOrchestrator, orch("50%"), "1m", false, "50%"},
		{"project window wins over env", KindOrchestrator, orch("1m"), "", true, "1m"},
		{"project env window is left alone", KindOrchestrator, ProjectConfig{}, "", true, ""},
		{"off leaves Auto", KindOrchestrator, orch(AutoCompactOff), "", false, ""},
		{"off still honors Settings", KindOrchestrator, orch(AutoCompactOff), "70%", false, "70%"},
	} {
		if got := DefaultAutoCompactChoice(tc.kind, tc.project, tc.harness, tc.envWindow); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestProjectConfigValidatesOrchestratorCompactWindow(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"", "200k", "1m", "40%", AutoCompactOff} {
		if err := (ProjectConfig{Orchestrator: RoleOverride{AutoCompactWindow: ok}}).Validate(); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"150000", "5%", "on"} {
		if err := (ProjectConfig{Orchestrator: RoleOverride{AutoCompactWindow: bad}}).Validate(); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if err := (ProjectConfig{Worker: RoleOverride{AutoCompactWindow: "200k"}}).Validate(); err == nil {
		t.Error("worker.autoCompactWindow accepted; workers keep the Settings default")
	}
}
