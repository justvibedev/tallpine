package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

// Each layer is capped on its own, then the project layer is cut so both
// together fit MaxLayeredRulesBytes; every cut is marked and names its setting.
func TestLayerRulesCapsEachLayerAndTheCombination(t *testing.T) {
	small := LayerRules(RoleReviewer, " app ", " project ", "reviewerRules")
	if small.App != "app" || small.Project != "project" || small.Capped {
		t.Fatalf("small rules changed: %+v", small)
	}

	app := LayerRules(SkillRoleWorker, strings.Repeat("a", MaxAgentRulesBytes+10), "p", "agentRules")
	if !app.Capped || !strings.HasSuffix(app.App, "shorten app.workerRules]") || strings.Contains(app.App, strings.Repeat("a", MaxAgentRulesBytes+1)) || app.Project != "p" {
		t.Fatalf("app layer not capped on its own: capped=%v project=%q tail=%q", app.Capped, app.Project, app.App[len(app.App)-60:])
	}

	both := LayerRules(SkillRoleOrchestrator, strings.Repeat("a", MaxAgentRulesBytes), strings.Repeat("p", MaxAgentRulesBytes), "orchestratorRules")
	if !both.Capped || both.App != strings.Repeat("a", MaxAgentRulesBytes) {
		t.Fatalf("app layer within its cap was cut: capped=%v", both.Capped)
	}
	if !strings.Contains(both.Project, "[project rules truncated at") || !strings.Contains(both.Project, "shorten orchestratorRules") {
		t.Fatalf("project layer marker = %q", both.Project[len(both.Project)-120:])
	}
	kept := strings.Count(both.Project, "p") - strings.Count("[project rules truncated; shorten orchestratorRules (app and project rules together are capped)]", "p")
	if len(both.App)+kept > MaxLayeredRulesBytes {
		t.Fatalf("combined rules %d bytes exceed %d", len(both.App)+kept, MaxLayeredRulesBytes)
	}
}

// Only worker, orchestrator and reviewer switches with names are valid, and
// app rules over the cap are refused.
func TestAppInstructionsValidate(t *testing.T) {
	if err := (AppInstructions{SkillRoles: SkillRoles{"worker": {"a": true}, "reviewer": {"b": false}}}).Validate(); err != nil {
		t.Fatal(err)
	}
	for name, a := range map[string]AppInstructions{
		"role":  {SkillRoles: SkillRoles{"planner": {"a": true}}},
		"name":  {SkillRoles: SkillRoles{"worker": {" ": true}}},
		"rules": {ReviewerRules: strings.Repeat("r", MaxAgentRulesBytes+1)},
	} {
		if err := a.Validate(); err == nil {
			t.Errorf("%s: invalid app instructions accepted", name)
		}
	}
}

// The app rules and switches are stored inline with the workflow defaults and
// read back; an old stored object without them reads as none.
func TestWorkflowDefaultsStoresAppInstructions(t *testing.T) {
	d := DefaultWorkflowDefaults()
	d.AppInstructions = AppInstructions{WorkerRules: "w", ReviewerRules: "r", SkillRoles: SkillRoles{"worker": {"x": false}}}
	raw, err := d.EncodeStored()
	if err != nil {
		t.Fatal(err)
	}
	var back WorkflowDefaults
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.WorkerRules != "w" || back.ReviewerRules != "r" || back.SkillRoles["worker"]["x"] {
		t.Fatalf("round trip lost app instructions: %s", raw)
	}
	var old WorkflowDefaults
	if err := json.Unmarshal([]byte(`{"autoReview":true}`), &old); err != nil || old.WorkerRules != "" || old.SkillRoles != nil {
		t.Fatalf("old object: %+v %v", old.AppInstructions, err)
	}
}
