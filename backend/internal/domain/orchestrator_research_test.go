package domain

import "testing"

func TestResearchCompactChoice(t *testing.T) {
	cfg := func(w string) ProjectConfig {
		return ProjectConfig{Orchestrator: RoleOverride{ResearchCompactWindow: w}}
	}
	for _, tc := range []struct {
		name    string
		project ProjectConfig
		global  bool
		want    string
		ok      bool
	}{
		{"default 250k", cfg(""), true, "250k", true},
		{"global off", cfg(""), false, "", false},
		{"project off", cfg(AutoCompactOff), true, "", false},
		{"custom size", cfg("300k"), true, "300k", true},
		{"percent", cfg("30%"), true, "30%", true},
	} {
		got, ok := ResearchCompactChoice(tc.project, tc.global)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s: got (%q, %v), want (%q, %v)", tc.name, got, ok, tc.want, tc.ok)
		}
	}
}

func TestResearchCompactWindowValidation(t *testing.T) {
	ok := ProjectConfig{Orchestrator: RoleOverride{ResearchCompactWindow: "off"}}
	if err := ok.validateResearch(); err != nil {
		t.Fatalf("off: %v", err)
	}
	for _, bad := range []ProjectConfig{
		{Orchestrator: RoleOverride{ResearchCompactWindow: "huge"}},
		{Worker: RoleOverride{ResearchCompactWindow: "250k"}},
		{Planner: RoleOverride{ResearchCompactWindow: "250k"}},
	} {
		if err := bad.validateResearch(); err == nil {
			t.Errorf("%+v: want an error", bad)
		}
	}
	for _, good := range []string{"", "250k", "1m", "30%"} {
		if err := (ProjectConfig{Orchestrator: RoleOverride{ResearchCompactWindow: good}}).validateResearch(); err != nil {
			t.Errorf("%q: %v", good, err)
		}
	}
}

func TestNextOrchestratorWindow(t *testing.T) {
	const opus, sonnet = 1_000_000, 200_000
	for _, tc := range []struct {
		name    string
		current int
		model   int64
		want    string
		ok      bool
	}{
		{"coordination 120k steps to 250k", 120_000, opus, "250k", true},
		{"floored 151k steps to 250k", 151_000, opus, "250k", true},
		{"250k steps to 400k", 250_000, opus, "400k", true},
		{"400k steps to 1m", 400_000, opus, "1m", true},
		{"at the agent default (1m) no step", 0, opus, "", false},
		{"next rung does not fit a 200k model", 120_000, sonnet, "", false},
		{"1m does not fit a 500k model", 400_000, 500_000, "", false},
		{"unknown model window", 120_000, 0, "", false},
	} {
		got, ok := NextOrchestratorWindow(tc.current, tc.model)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s: NextOrchestratorWindow(%d, %d) = %q, %v; want %q, %v", tc.name, tc.current, tc.model, got, ok, tc.want, tc.ok)
		}
	}
}

func TestResearchWindowFlag(t *testing.T) {
	for _, w := range OrchestratorWindowLadder {
		if !ValidResearchWindow(w) || !ResearchWindowFits(w, 1_000_000) {
			t.Errorf("%s: want valid and fitting a 1M model", w)
		}
	}
	for _, w := range []string{"", "120k", "40%", "2m"} {
		if ValidResearchWindow(w) {
			t.Errorf("%q: want rejected", w)
		}
	}
	if ResearchWindowFits("400k", 200_000) || ResearchWindowFits("250k", 200_000) {
		t.Error("a rung above a 200k model must not fit")
	}
	if !ResearchWindowFits("400k", 0) {
		t.Error("an unknown model window keeps the explicit size")
	}
}
