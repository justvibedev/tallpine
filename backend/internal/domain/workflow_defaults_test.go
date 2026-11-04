package domain

import "testing"

func TestEffectiveAutoReview(t *testing.T) {
	yes, no := true, false
	for _, tc := range []struct {
		name string
		cfg  *bool
		app  bool
		want bool
	}{
		{"nil inherits on", nil, true, true},
		{"nil inherits off", nil, false, false},
		{"explicit true beats off", &yes, false, true},
		{"explicit false beats on", &no, true, false},
		{"explicit true on on", &yes, true, true},
		{"explicit false on off", &no, false, false},
	} {
		got := ProjectConfig{AutoReview: tc.cfg}.EffectiveAutoReview(WorkflowDefaults{AutoReview: tc.app})
		if got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

func TestEffectivePlanningMode(t *testing.T) {
	app := WorkflowDefaults{PlanningMode: PlanningAlways}
	for project, want := range map[PlanningMode]PlanningMode{"": PlanningAlways, PlanningAuto: PlanningAuto, PlanningOff: PlanningOff} {
		if got := (ProjectConfig{PlanningMode: project}).EffectivePlanningMode(app); got != want {
			t.Errorf("project %q = %q, want %q", project, got, want)
		}
	}
	if got := (ProjectConfig{}).EffectivePlanningMode(WorkflowDefaults{}); got != PlanningAuto {
		t.Errorf("no value anywhere = %q, want auto", got)
	}
}

func TestEffectivePlanAutoApprove(t *testing.T) {
	yes, no := true, false
	for _, c := range []struct {
		project *bool
		app     bool
		want    bool
	}{{nil, false, false}, {nil, true, true}, {&yes, false, true}, {&no, true, false}, {&yes, true, true}, {&no, false, false}} {
		got := (ProjectConfig{PlanAutoApprove: c.project}).EffectivePlanAutoApprove(WorkflowDefaults{PlanAutoApprove: c.app})
		if got != c.want {
			t.Errorf("project %v app %v = %v, want %v", c.project, c.app, got, c.want)
		}
	}
}
