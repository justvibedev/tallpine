package githubcreds

import (
	"tallpine.eu/tallpine/backend/internal/agentpolicy"
)

// tokenVariables are the environment variables that carry a GitHub credential.
var tokenVariables = []string{"GH_TOKEN", "GITHUB_TOKEN", "TALLPINE_GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN"}

// Outcome is the GitHub credential mode decided for a session and the effects
// its seams apply. The zero Outcome is mode env: nothing changes.
type Outcome struct {
	mode Mode
}

// Record implements agentpolicy.Outcome. The mode and its source are already in
// the decision record.
func (Outcome) Record() map[string]any { return nil }

// Of returns the GitHub credential outcome of a decision; the zero Decision
// yields mode env.
func Of(d agentpolicy.Decision) Outcome {
	if out, ok := d.Outcome(Name); ok {
		if typed, ok := out.(Outcome); ok {
			return typed
		}
	}
	return Outcome{mode: Env}
}

// Mode is the decided mode.
func (o Outcome) Mode() Mode {
	if o.mode == "" {
		return Env
	}
	return o.mode
}

// UsesHelper reports whether the session gets the git credential helper and the
// gh shim instead of a token in its environment.
func (o Outcome) UsesHelper() bool { return o.Mode() == Helper }

// Denied reports whether every credential request of the session is refused.
func (o Outcome) Denied() bool { return o.Mode() == None }

// KeepsTokensOutOfEnv reports whether the session's environment must carry no
// GitHub token the daemon would otherwise add or inherit: no auto-picked account
// token, and the daemon's own GH_TOKEN and friends unset. A token the project's
// own environment sets is the user's explicit choice and is kept.
func (o Outcome) KeepsTokensOutOfEnv() bool { return o.Mode() != Env }

// WithheldVariables names the variables to unset in the agent's inherited
// environment under this mode, or nil under mode env.
func (o Outcome) WithheldVariables() []string {
	if !o.KeepsTokensOutOfEnv() {
		return nil
	}
	return append([]string(nil), tokenVariables...)
}
