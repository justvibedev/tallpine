// Package devmode is the owner-set Dev mode switch. When on, a scoped
// orchestrator token may do anything the owner can in the app (cross-project
// orchestrator start and config, any setting including protectState, which is
// also treated as off), except change Dev mode itself. It is a grant, so the
// default is off; only the user layer sets it (never a project or an agent
// token), and a managed policy can lock it off.
//
// The daemon enforces the grant (httpd/agent_scope.go, service/settingswrite);
// the protectState rule reads agentpolicy.Input.DevMode.
package devmode

import (
	"encoding/json"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
)

// Name is the rule's key in settings documents.
const Name = agentpolicy.DevModeRule

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
			Name:            FieldEnabled,
			Kind:            agentpolicy.KindBool,
			Default:         json.RawMessage(`false`),
			LabelKey:        "settings.agentPolicy.devMode.label",
			DescriptionKey:  "settings.agentPolicy.devMode.description",
			ProjectOverride: agentpolicy.ProjectNever,
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
