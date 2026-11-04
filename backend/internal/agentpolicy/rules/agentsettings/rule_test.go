package agentsettings

import (
	"encoding/json"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
)

func settings(mode string, allow, deny []string) agentpolicy.Settings {
	a, _ := json.Marshal(allow)
	d, _ := json.Marshal(deny)
	m, _ := json.Marshal(mode)
	return agentpolicy.Settings{FieldMode: m, FieldAllow: a, FieldDeny: d}
}

func TestMatchesKeysGroupsAndGlobs(t *testing.T) {
	for _, c := range []struct {
		pattern, key string
		want         bool
	}{
		{"*", "project.env", true},
		{"project.env", "project.env", true},
		{"project.orchestrator", "project.orchestrator.idleCompactAt", true},
		{"project.*", "project.orchestrator.idleCompactAt", true},
		{"policy.*", "project.env", false},
		{"project.env", "project.envx", false},
		{"project.orchestrator", "project.orchestratorRules", false},
	} {
		if got := Matches(c.pattern, c.key); got != c.want {
			t.Errorf("Matches(%q, %q) = %v, want %v", c.pattern, c.key, got, c.want)
		}
	}
}

// B202 PR8: the default allows, deny turns every agent write off, the deny
// list wins, and allowList admits only listed keys.
func TestRefusal(t *testing.T) {
	r := Rule{}
	for _, c := range []struct {
		name        string
		s           agentpolicy.Settings
		key         string
		wantRefused bool
	}{
		{"default allows", settings("allow", nil, nil), "project.postCreate", false},
		{"deny mode", settings("deny", []string{"*"}, nil), "project.agentRules", true},
		{"deny list", settings("allow", nil, []string{"project.postCreate", "project.env"}), "project.env", true},
		{"deny list other key", settings("allow", nil, []string{"project.env"}), "project.agentRules", false},
		{"allowList listed", settings("allowList", []string{"project.orchestrator"}, nil), "project.orchestrator.idleCompactAt", false},
		{"allowList unlisted", settings("allowList", []string{"project.orchestrator"}, nil), "project.verifyCommand", true},
		{"bad pattern denies", settings("allow", nil, []string{"postCreate"}), "project.postCreate", true},
		{"bad allow pattern denies", settings("allowList", []string{"agentRules"}, nil), "project.agentRules", true},
		{"deny beats allow", settings("allowList", []string{"project.*"}, []string{"project.reviewers"}), "project.reviewers", true},
	} {
		got := r.Decide(agentpolicy.Input{}, c.s).(Outcome).Refusal(c.key)
		if (got != "") != c.wantRefused {
			t.Errorf("%s: Refusal(%s) = %q", c.name, c.key, got)
		}
	}
	if Of(agentpolicy.Decision{}).Refusal("project.env") != "" {
		t.Error("the zero decision refused")
	}
}

func TestValidateRejectsUnknownPatterns(t *testing.T) {
	r := Rule{}
	if err := r.Validate(settings("allow", []string{"project.*", "policy.permissionCap"}, []string{"*"})); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"env", "project.", "project..env", "projects.env", "project/env"} {
		if r.Validate(settings("allow", nil, []string{bad})) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
