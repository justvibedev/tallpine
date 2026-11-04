// Package securitymode is the one switch that raises the install's baseline:
// when on, protectState, envFilter and githubCreds are tightened to a floor
// (source security_mode) for every session that can take it. It changes no
// grant, permission mode or permission cap. Off by default in this version; a
// project may switch it off for itself (project beats user) or on when the
// install has it off. A managed value locks it; the hardened profile implies on.
//
// allowDocker is the project opt-in that leaves the Docker socket reachable
// inside the fence; it gives the agent full host access, so only the user
// (never an agent) may set it.
package securitymode

import (
	"encoding/json"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
)

// Names of the rule and its fields.
const (
	Name             = agentpolicy.SecurityModeRule
	FieldEnabled     = agentpolicy.SecurityModeEnabled
	FieldAllowDocker = agentpolicy.SecurityModeAllowDocker
)

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
			EnvAliases:     []string{"TALLPINE_AGENT_SECURITY_MODE"},
			LabelKey:       "settings.agentPolicy.securityMode.label",
			DescriptionKey: "settings.agentPolicy.securityMode.description",
			// A project may opt out (off beats the install's on) or in.
			ProjectOverride: agentpolicy.ProjectAny,
			// A typo in an administrator's value turns the mode on, with a warning.
			OnInvalid: agentpolicy.InvalidStrictest,
		}, {
			Name:            FieldAllowDocker,
			Kind:            agentpolicy.KindBool,
			Default:         json.RawMessage(`false`),
			LabelKey:        "settings.agentPolicy.securityMode.allowDocker",
			DescriptionKey:  "settings.agentPolicy.securityMode.allowDockerDescription",
			ProjectOverride: agentpolicy.ProjectAny,
		}},
	}
}

// Validate implements agentpolicy.Rule.
func (Rule) Validate(agentpolicy.Settings) error { return nil }

// Decide implements agentpolicy.Rule.
func (Rule) Decide(_ agentpolicy.Input, s agentpolicy.Settings) agentpolicy.Outcome {
	return Outcome{on: s.Bool(FieldEnabled), allowDocker: s.Bool(FieldAllowDocker)}
}

// Outcome is whether Security mode is on for the session. The zero Outcome is off.
type Outcome struct {
	on          bool
	allowDocker bool
}

// Record implements agentpolicy.Outcome; the settings and sources are already
// in the decision record.
func (Outcome) Record() map[string]any { return nil }

// On reports whether Security mode is on.
func (o Outcome) On() bool { return o.on }

// AllowDocker reports whether the fence leaves the Docker socket reachable.
func (o Outcome) AllowDocker() bool { return o.allowDocker }

// Of returns the outcome of a decision; the zero Decision is off.
func Of(d agentpolicy.Decision) Outcome {
	if out, ok := d.Outcome(Name); ok {
		if typed, ok := out.(Outcome); ok {
			return typed
		}
	}
	return Outcome{}
}
