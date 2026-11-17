package domain

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// AppInstructions are the app-wide standing rules and skill switches every
// project inherits (B740 a15). They are stored with the workflow defaults;
// people write them, no agent token can.
type AppInstructions struct {
	// WorkerRules, OrchestratorRules and ReviewerRules are prepended to the
	// project's rules of that role under their own heading.
	WorkerRules       string `json:"workerRules,omitempty"`
	OrchestratorRules string `json:"orchestratorRules,omitempty"`
	ReviewerRules     string `json:"reviewerRules,omitempty"`
	// SkillRoles are the app layer of the skill switches: a project's own
	// switch for a skill wins, a skill it leaves unset follows this one.
	SkillRoles SkillRoles `json:"skillRoles,omitempty"`
}

// MaxLayeredRulesBytes caps one role's app and project rules together. Each
// layer is first capped at MaxAgentRulesBytes on its own.
const MaxLayeredRulesBytes = 12 << 10

// RulesFor is the app rules of a session role ("" for an unknown role).
func (a AppInstructions) RulesFor(role string) string {
	switch role {
	case SkillRoleWorker:
		return a.WorkerRules
	case SkillRoleOrchestrator:
		return a.OrchestratorRules
	case RoleReviewer:
		return a.ReviewerRules
	}
	return ""
}

// AppRulesKey is the app setting holding a role's rules, named in cap notes.
func AppRulesKey(role string) string {
	if role == SkillRoleWorker {
		return "app.workerRules"
	}
	return "app." + role + "Rules"
}

// LayeredRules is one role's rules from both layers, each already capped.
type LayeredRules struct {
	App     string
	Project string
	// Capped says a layer or the combined text was cut.
	Capped bool
}

// LayerRules caps the app rules of role at MaxAgentRulesBytes, then cuts the
// project rules (already capped by the caller) so both fit
// MaxLayeredRulesBytes. Every cut says so inside the text and names the
// setting to shorten. projectLabel names the project settings.
func LayerRules(role, app, project, projectLabel string) LayeredRules {
	app = strings.TrimSpace(app)
	project = strings.TrimSpace(project)
	out := LayeredRules{}
	out.App, out.Capped = capRulesAt(app, MaxAgentRulesBytes, "app", AppRulesKey(role))
	room := MaxLayeredRulesBytes - len(out.App)
	var cut bool
	out.Project, cut = capRulesAt(project, room, "project", projectLabel+" (app and project rules together are capped at "+fmt.Sprint(MaxLayeredRulesBytes)+" bytes)")
	out.Capped = out.Capped || cut
	return out
}

// capRulesAt is CapRules with its own limit and layer name in the marker.
func capRulesAt(rules string, limit int, layer, label string) (string, bool) {
	if len(rules) <= limit {
		return rules, false
	}
	cut := max(limit, 0)
	for cut > 0 && !utf8.RuneStart(rules[cut]) {
		cut--
	}
	return rules[:cut] + fmt.Sprintf("\n\n[%s rules truncated at %d bytes; shorten %s]", layer, cut, label), true
}

// Validate refuses rules over MaxAgentRulesBytes and switches for a role that
// does not exist.
func (a AppInstructions) Validate() error {
	for _, role := range SkillRoleNames {
		if n := len(a.RulesFor(role)); n > MaxAgentRulesBytes {
			return fmt.Errorf("%s is %d bytes; the limit is %d", AppRulesKey(role), n, MaxAgentRulesBytes)
		}
	}
	return a.SkillRoles.Validate()
}

// validSkillKey reports whether key is a role, the master switch or the
// switch of a known agent type.
func validSkillKey(key string) bool {
	if slices.Contains(SkillRoleNames, key) || key == SkillMasterKey {
		return true
	}
	for _, k := range skillAgentKeys {
		if key == k {
			return true
		}
	}
	return false
}

// Validate refuses a role other than worker, orchestrator or reviewer and an
// empty skill name.
func (r SkillRoles) Validate() error {
	for role, skills := range r {
		if !validSkillKey(role) {
			return fmt.Errorf("unknown role %q; use worker, orchestrator, reviewer, %s or an agentXxx key", role, SkillMasterKey)
		}
		for name := range skills {
			if strings.TrimSpace(name) == "" {
				return fmt.Errorf("role %s has an empty skill name", role)
			}
		}
	}
	return nil
}
