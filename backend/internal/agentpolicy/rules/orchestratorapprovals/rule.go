// Package orchestratorapprovals is the rule that lets a project's orchestrator
// answer the permission prompts of that project's workers. It is a grant, not a
// restriction, so the default is off: an untouched install keeps today's
// behaviour, where only the human resolves an approval. A managed policy file
// can force it off for every project (the managed layer outranks the project
// and user layers), which is how an organization keeps the human in the loop.
package orchestratorapprovals

import (
	"encoding/json"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
)

// Name is the rule's key in settings documents.
const Name = "orchestratorApprovals"

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
			LabelKey:       "settings.agentPolicy.orchestratorApprovals.label",
			DescriptionKey: "settings.agentPolicy.orchestratorApprovals.description",
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

// Outcome is whether the orchestrator may answer worker prompts. The zero
// Outcome is off.
type Outcome struct {
	enabled bool
}

// Record implements agentpolicy.Outcome; the setting and its source are
// already in the decision record.
func (Outcome) Record() map[string]any { return nil }

// Enabled reports whether the orchestrator may answer worker prompts.
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
