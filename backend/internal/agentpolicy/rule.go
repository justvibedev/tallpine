// Package agentpolicy is the enterprise agent policy: a registry of rules that
// each restrict one aspect of what an agent may do (the permission cap today;
// environment, GitHub credentials, network egress later), a layered settings
// model (managed > env > project > user > default) and a per-session immutable
// Decision that the enforcement seams read. The zero Decision restricts
// nothing, so an install with no managed file, no TALLPINE_AGENT_* variable and
// untouched settings behaves exactly as before.
//
// A rule is one package under rules/: a Schema (its settings, defaults and
// validation, which also drive the API, CLI and Settings UI), a pure Decide
// step, and the effect methods on its Outcome that seams call. See
// docs/agent-policy.md.
package agentpolicy

import (
	"encoding/json"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// Rule is one restriction. Implementations live in their own package under
// rules/ and are listed in rules.All; the engine knows no rule by name.
type Rule interface {
	// Name is the stable key of the rule in settings documents, e.g. "permissionCap".
	Name() string
	// Schema describes the rule's settings.
	Schema() Schema
	// Validate checks relations between fields that the schema cannot express.
	// It receives the fully merged settings, defaults included.
	Validate(Settings) error
	// Decide turns the merged settings into the rule's Outcome. It is pure: no
	// I/O, no clock, no globals, so every rule is table-testable.
	Decide(Input, Settings) Outcome
}

// Outcome is what a rule decided for one session. Each rule defines its own
// concrete type with the effect methods its seams call, and an accessor
// (`permissioncap.Of(decision)`) that returns the typed value.
type Outcome interface {
	// Record returns the secret-free facts to persist and log for this rule.
	Record() map[string]any
}

// SessionRef identifies what a decision is for.
type SessionRef struct {
	ID      domain.SessionID    `json:"id,omitempty"`
	Project domain.ProjectID    `json:"project,omitempty"`
	Harness domain.AgentHarness `json:"harness,omitempty"`
	Kind    domain.SessionKind  `json:"kind,omitempty"`
}

// Input is everything a rule's Decide may depend on besides its settings.
type Input struct {
	Session SessionRef
	// Profile is the managed profile in force ("" for none).
	Profile string
	// DevMode is the owner-set devMode policy (policy.devMode.enabled) in force
	// for this session. protectState reads it; it is false under a managed lock.
	DevMode bool
}

// Settings holds the values of one rule's fields, by field name, as JSON.
type Settings map[string]json.RawMessage

// String returns a string field's value, "" when absent or not a string.
func (s Settings) String(name string) string {
	var v string
	_ = json.Unmarshal(s[name], &v)
	return v
}

// Strings returns a string-list field's value.
func (s Settings) Strings(name string) []string {
	var v []string
	_ = json.Unmarshal(s[name], &v)
	return v
}

// Bool returns a bool field's value.
func (s Settings) Bool(name string) bool {
	var v bool
	_ = json.Unmarshal(s[name], &v)
	return v
}

// Int returns an int field's value.
func (s Settings) Int(name string) int {
	var v int
	_ = json.Unmarshal(s[name], &v)
	return v
}

func (s Settings) clone() Settings {
	out := make(Settings, len(s))
	for k, v := range s {
		out[k] = v
	}
	return out
}
