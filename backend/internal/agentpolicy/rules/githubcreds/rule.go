// Package githubcreds is the rule that decides how an agent gets GitHub
// credentials. Today's behaviour, mode env, puts a token in the agent's
// environment (GH_TOKEN). Mode helper keeps tokens out of the environment: git
// asks a credential helper and `gh` runs through a shim, each asking the daemon
// for the project's token per use, with an audit line and the ability to deny.
// Mode none refuses every request. See docs/agent-policy.md.
//
// A helper does not stop an agent from invoking it. What it removes is the token
// sitting in env dumps, logs, crash reports and the environment of every child
// process of every tool the agent runs.
package githubcreds

import (
	"encoding/json"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
)

// Name is the rule's key in settings documents.
const Name = "githubCreds"

// FieldMode is the credential mode field.
const FieldMode = "mode"

// Mode is how agents obtain GitHub credentials.
type Mode string

// Modes in ascending strictness. Env is today's behaviour.
const (
	Env    Mode = "env"
	Helper Mode = "helper"
	None   Mode = "none"
)

var modes = []string{string(Env), string(Helper), string(None)}

// Rule implements agentpolicy.Rule.
type Rule struct{}

// Name implements agentpolicy.Rule.
func (Rule) Name() string { return Name }

// Schema implements agentpolicy.Rule.
func (Rule) Schema() agentpolicy.Schema {
	return agentpolicy.Schema{
		TitleKey: "settings.agentPolicy.title",
		Fields: []agentpolicy.Field{{
			Name:           FieldMode,
			Kind:           agentpolicy.KindEnum,
			Enum:           modes,
			Default:        json.RawMessage(`"env"`),
			LabelKey:       "settings.agentPolicy.github.label",
			DescriptionKey: "settings.agentPolicy.github.description",
			OptionLabelKeys: map[string]string{
				string(Env):    "settings.agentPolicy.github.env",
				string(Helper): "settings.agentPolicy.github.helper",
				string(None):   "settings.agentPolicy.github.none",
			},
			ProjectOverride: agentpolicy.ProjectTightenOnly,
			// An invalid value is reported and ignored: failing closed to "none"
			// over a typo would cut every agent off from GitHub.
			OnInvalid: agentpolicy.InvalidIgnore,
		}},
	}
}

// Validate implements agentpolicy.Rule.
func (Rule) Validate(agentpolicy.Settings) error { return nil }

// Decide implements agentpolicy.Rule.
func (Rule) Decide(_ agentpolicy.Input, s agentpolicy.Settings) agentpolicy.Outcome {
	return Outcome{mode: parse(s.String(FieldMode))}
}

func parse(v string) Mode {
	for _, m := range modes {
		if v == m {
			return Mode(v)
		}
	}
	return Env
}
