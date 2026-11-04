// Package orchestratorlanes is the rule that lets a project's main (non-lane)
// orchestrator start lane orchestrators and close them itself. It is a grant,
// so the default is off: only the user starts a lane until a project opts in.
// A managed policy file can force it off for every project.
//
// The daemon enforces what the grant covers (httpd/agent_scope_lanes.go); this
// package only carries the setting.
package orchestratorlanes

import (
	"encoding/json"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
)

// Name is the rule's key in settings documents.
const Name = "orchestratorLanes"

// FieldEnabled is the on/off field.
const FieldEnabled = "enabled"

// Rule implements agentpolicy.Rule.
type Rule struct{}

// Name implements agentpolicy.Rule.
func (Rule) Name() string { return Name }

// Schema implements agentpolicy.Rule.
func (Rule) Schema() agentpolicy.Schema {
	return agentpolicy.Schema{
		TitleKey: "settings.agentPolicy.title",
		Fields: []agentpolicy.Field{{
			Name:           FieldEnabled,
			Kind:           agentpolicy.KindBool,
			Default:        json.RawMessage(`false`),
			LabelKey:       "settings.agentPolicy.orchestratorLanes.label",
			DescriptionKey: "settings.agentPolicy.orchestratorLanes.description",
			// A project opts in; tighten-only does not fit a grant whose default is off.
			ProjectOverride: agentpolicy.ProjectAny,
		}},
	}
}

// Validate implements agentpolicy.Rule.
func (Rule) Validate(agentpolicy.Settings) error { return nil }

// Decide implements agentpolicy.Rule.
func (Rule) Decide(_ agentpolicy.Input, s agentpolicy.Settings) agentpolicy.Outcome {
	return Outcome{enabled: s.Bool(FieldEnabled)}
}

// Outcome is whether the main orchestrator may start and close lanes. The zero
// Outcome is off.
type Outcome struct {
	enabled bool
}

// Record implements agentpolicy.Outcome.
func (Outcome) Record() map[string]any { return nil }

// Enabled reports whether the main orchestrator may start and close lanes.
func (o Outcome) Enabled() bool { return o.enabled }

// Of returns the outcome of a decision; the zero Decision is off.
func Of(d agentpolicy.Decision) Outcome {
	if out, ok := d.Outcome(Name); ok {
		if typed, ok := out.(Outcome); ok {
			return typed
		}
	}
	return Outcome{}
}
