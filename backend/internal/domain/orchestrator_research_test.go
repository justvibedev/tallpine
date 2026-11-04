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
