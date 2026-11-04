// Package repoagentconfig is the rule that decides whether a worker's agent
// loads configuration committed to the repository it works in (project settings,
// hooks, MCP servers). The default is on, because a project's own committed
// config is usually wanted. A project may turn it off, and a managed policy
// file can force it off for every project (the managed layer outranks the
// project and user layers), which is how an organization keeps a hostile PR
// head from running commands through repo-committed agent config. Reviewer
// launches never load it, whatever this rule says.
package repoagentconfig

import (
	"encoding/json"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
)

// Name is the rule's key in settings documents.
const Name = "repoAgentConfig"

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
			Default:        json.RawMessage(`true`),
			LabelKey:       "settings.agentPolicy.repoAgentConfig.label",
			DescriptionKey: "settings.agentPolicy.repoAgentConfig.description",
			// A project may switch its own repo config off; the managed layer
			// forces it off everywhere.
			ProjectOverride: agentpolicy.ProjectAny,
		}},
	}
}

// Validate implements agentpolicy.Rule.
func (Rule) Validate(agentpolicy.Settings) error { return nil }

// Decide implements agentpolicy.Rule.
func (Rule) Decide(_ agentpolicy.Input, s agentpolicy.Settings) agentpolicy.Outcome {
	return Outcome{disabled: !s.Bool(FieldEnabled)}
}

// Outcome is whether the agent loads the repository's agent config. The zero
// Outcome loads it, matching the rule's default.
type Outcome struct {
	disabled bool
}

// Record implements agentpolicy.Outcome; the setting and its source are
// already in the decision record.
func (Outcome) Record() map[string]any { return nil }

// Enabled reports whether the agent loads repo-committed agent config.
func (o Outcome) Enabled() bool { return !o.disabled }

// Of returns the outcome of a decision; the zero Decision loads repo config.
func Of(d agentpolicy.Decision) Outcome {
	if out, ok := d.Outcome(Name); ok {
		if typed, ok := out.(Outcome); ok {
			return typed
		}
	}
	return Outcome{}
}
