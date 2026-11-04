// Package crossprojectorchestration is the rule that lets a project's
// orchestrator coordinate with other projects' orchestrators and create
// projects of its own. It is a grant, not a restriction, so the default is off:
// an untouched install keeps a scoped orchestrator confined to its project. A
// managed policy file can force it off for every project (the managed layer
// outranks the project and user layers).
//
// The daemon enforces what the grant covers (httpd/agent_scope.go); this
// package only carries the setting.
package crossprojectorchestration

import (
	"encoding/json"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
)

// Name is the rule's key in settings documents.
const Name = "crossProjectOrchestration"

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
			LabelKey:       "settings.agentPolicy.crossProjectOrchestration.label",
			DescriptionKey: "settings.agentPolicy.crossProjectOrchestration.description",
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

// Outcome is whether the orchestrator may coordinate across projects. The zero
// Outcome is off.
type Outcome struct {
	enabled bool
}

// Record implements agentpolicy.Outcome; the setting and its source are
// already in the decision record.
func (Outcome) Record() map[string]any { return nil }

// Enabled reports whether the orchestrator may coordinate across projects.
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
