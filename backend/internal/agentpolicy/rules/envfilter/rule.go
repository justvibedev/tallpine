// Package envfilter is the rule that decides which of the daemon's environment
// variables an agent inherits. Mode inherit (the default) is today's behaviour:
// the agent starts with the daemon's whole environment. Mode allowlist starts it
// with only a curated base list (what git, gh, ssh, node, go and the common
// toolchains need), the variables the supported agent CLIs read for their own
// login, the user's passthrough list, and what the daemon itself sets for the
// session (VO_*, the project's environment, account variables).
//
// It limits ambient credentials, not what an allowed agent can do. See
// docs/agent-policy.md.
package envfilter

import (
	"encoding/json"
	"fmt"
	"regexp"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
)

// Name is the rule's key in settings documents.
const Name = "envFilter"

// Field names.
const (
	FieldMode        = "mode"
	FieldPassthrough = "passthrough"
)

// Mode is how an agent gets its environment.
type Mode string

// Modes in ascending strictness. Inherit is today's behaviour.
const (
	Inherit   Mode = "inherit"
	Allowlist Mode = "allowlist"
)

// maxPassthrough bounds the passthrough list (the schema's own limit is the
// same; this names it for the validation message).
const maxPassthrough = 100

var passthroughName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_()]*\*?$`)

// Rule implements agentpolicy.Rule.
type Rule struct{}

// Name implements agentpolicy.Rule.
func (Rule) Name() string { return Name }

// Schema implements agentpolicy.Rule.
func (Rule) Schema() agentpolicy.Schema {
	return agentpolicy.Schema{
		TitleKey: "settings.agentPolicy.title",
		Fields: []agentpolicy.Field{
			{
				Name:           FieldMode,
				Kind:           agentpolicy.KindEnum,
				Enum:           []string{string(Inherit), string(Allowlist)},
				Default:        json.RawMessage(`"inherit"`),
				EnvAliases:     []string{"TALLPINE_AGENT_ENV_MODE"},
				LegacyKey:      "envMode",
				LabelKey:       "settings.agentPolicy.envFilter.label",
				DescriptionKey: "settings.agentPolicy.envFilter.description",
				OptionLabelKeys: map[string]string{
					string(Inherit):   "settings.agentPolicy.envFilter.inherit",
					string(Allowlist): "settings.agentPolicy.envFilter.allowlist",
				},
				ProjectOverride: agentpolicy.ProjectTightenOnly,
				// An invalid value is reported and ignored: failing closed to the
				// allowlist over a typo could cut agents off from their own tools.
				OnInvalid: agentpolicy.InvalidIgnore,
			},
			{
				Name:           FieldPassthrough,
				Kind:           agentpolicy.KindStringList,
				Default:        json.RawMessage(`[]`),
				EnvAliases:     []string{"TALLPINE_AGENT_ENV_PASSTHROUGH"},
				LegacyKey:      "envPassthrough",
				LabelKey:       "settings.agentPolicy.envFilter.passthrough",
				DescriptionKey: "settings.agentPolicy.envFilter.passthroughDescription",
				// Adding names loosens the allowlist, so a project cannot; a project's
				// own environment already reaches its agents.
				ProjectOverride: agentpolicy.ProjectNever,
				VisibleWhen:     &agentpolicy.Condition{Field: FieldMode, Equals: string(Allowlist)},
			},
		},
	}
}

// Validate implements agentpolicy.Rule: every passthrough entry is a variable
// name, optionally ending in * for a prefix.
func (Rule) Validate(s agentpolicy.Settings) error {
	names := s.Strings(FieldPassthrough)
	if len(names) > maxPassthrough {
		return fmt.Errorf("passthrough has %d entries; the limit is %d", len(names), maxPassthrough)
	}
	for _, name := range names {
		if !passthroughName.MatchString(name) {
			return fmt.Errorf("invalid passthrough entry %q (use a variable name, optionally ending in *)", name)
		}
	}
	return nil
}

// Decide implements agentpolicy.Rule.
func (Rule) Decide(in agentpolicy.Input, s agentpolicy.Settings) agentpolicy.Outcome {
	mode := Inherit
	if s.String(FieldMode) == string(Allowlist) {
		mode = Allowlist
	}
	// Under the hardened profile an empty or unknown harness gets no credential
	// list instead of the union of all of them.
	return Outcome{mode: mode, passthrough: s.Strings(FieldPassthrough), strict: in.Profile == agentpolicy.ProfileHardened}
}
