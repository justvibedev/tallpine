// Package agentsettings is the rule that decides which settings agents may
// change through the settings registry (B202). It is the organization's off
// switch for agent setting writes: an untouched install allows an
// orchestrator to change what the registry lets it write, a managed policy
// file can narrow that to a list of keys or groups, deny some, or turn agent
// writes off entirely. Workers never write settings; this rule only narrows.
package agentsettings

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
)

// Name is the rule's key in settings documents.
const Name = "agentSettings"

// Field names.
const (
	FieldMode  = "mode"
	FieldAllow = "allow"
	FieldDeny  = "deny"
)

// Modes in ascending strictness.
const (
	// ModeAllow lets agents change whatever the registry lets them write,
	// except keys the deny list names.
	ModeAllow = "allow"
	// ModeAllowList lets agents change only keys the allow list names (and the
	// deny list does not).
	ModeAllowList = "allowList"
	// ModeDeny refuses every agent setting change.
	ModeDeny = "deny"
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
			Name:           FieldMode,
			Kind:           agentpolicy.KindEnum,
			Enum:           []string{ModeAllow, ModeAllowList, ModeDeny},
			Default:        json.RawMessage(`"allow"`),
			LabelKey:       "settings.agentPolicy.agentSettings.label",
			DescriptionKey: "settings.agentPolicy.agentSettings.description",
			OptionLabelKeys: map[string]string{
				ModeAllow:     "settings.agentPolicy.agentSettings.allowMode",
				ModeAllowList: "settings.agentPolicy.agentSettings.allowListMode",
				ModeDeny:      "settings.agentPolicy.agentSettings.denyMode",
			},
			// Set by the user or an organization, never per project, so an
			// agent cannot widen or even touch its own switch.
			ProjectOverride: agentpolicy.ProjectNever,
			// A mistyped managed or env mode turns agent writes off rather
			// than leaving them open.
			OnInvalid: agentpolicy.InvalidStrictest,
		}, {
			Name:            FieldAllow,
			Kind:            agentpolicy.KindStringList,
			Default:         json.RawMessage(`[]`),
			LabelKey:        "settings.agentPolicy.agentSettings.allow",
			DescriptionKey:  "settings.agentPolicy.agentSettings.allowDescription",
			ProjectOverride: agentpolicy.ProjectNever,
		}, {
			Name:            FieldDeny,
			Kind:            agentpolicy.KindStringList,
			Default:         json.RawMessage(`[]`),
			LabelKey:        "settings.agentPolicy.agentSettings.deny",
			DescriptionKey:  "settings.agentPolicy.agentSettings.denyDescription",
			ProjectOverride: agentpolicy.ProjectNever,
		}},
	}
}

// patternRE accepts a key, a group (a key prefix) or a glob over keys.
var patternRE = regexp.MustCompile(`^(\*|(app|project|policy)(\.[A-Za-z0-9*]+)*)$`)

// Validate implements agentpolicy.Rule.
func (Rule) Validate(s agentpolicy.Settings) error {
	for _, field := range []string{FieldAllow, FieldDeny} {
		for _, p := range s.Strings(field) {
			if !patternRE.MatchString(p) {
				return fmt.Errorf("%s.%s: %q is not a setting key, group (project.orchestrator) or glob (project.*)", Name, field, p)
			}
		}
	}
	return nil
}

// Decide implements agentpolicy.Rule.
func (Rule) Decide(_ agentpolicy.Input, s agentpolicy.Settings) agentpolicy.Outcome {
	mode := s.String(FieldMode)
	if mode == "" {
		mode = ModeAllow
	}
	allow, deny := s.Strings(FieldAllow), s.Strings(FieldDeny)
	// A pattern that is not a key, group or glob would match nothing, so a
	// mistyped deny entry would silently leave its setting open. Whatever the
	// mode, and from whichever layer, it turns agent writes off.
	for _, p := range append(append([]string{}, allow...), deny...) {
		if !patternRE.MatchString(p) {
			mode = ModeDeny
		}
	}
	return Outcome{mode: mode, allow: allow, deny: deny, set: true}
}

// Outcome is which settings agents may change. The zero Outcome (no engine)
// allows, as the default does.
type Outcome struct {
	mode        string
	allow, deny []string
	set         bool
}

// Record implements agentpolicy.Outcome; the settings and their source are
// already in the decision record.
func (Outcome) Record() map[string]any { return nil }

// Refusal returns why an agent may not change key, or "" when it may.
func (o Outcome) Refusal(key string) string {
	switch {
	case !o.set:
		return ""
	case o.mode == ModeDeny:
		return "agent policy agentSettings turns agent setting changes off"
	case matchesAny(o.deny, key):
		return "agent policy agentSettings denies agents this setting"
	case o.mode == ModeAllowList && !matchesAny(o.allow, key):
		return "agent policy agentSettings does not allow agents this setting"
	}
	return ""
}

// Matches reports whether pattern names key: "*", the key itself, a group the
// key is under (project.orchestrator covers project.orchestrator.idleCompactAt)
// or a glob (project.*).
func Matches(pattern, key string) bool {
	if pattern == "*" || pattern == key || strings.HasPrefix(key, pattern+".") {
		return true
	}
	ok, err := path.Match(pattern, key)
	return err == nil && ok
}

func matchesAny(patterns []string, key string) bool {
	for _, p := range patterns {
		if Matches(p, key) {
			return true
		}
	}
	return false
}

// Of returns the outcome of a decision; the zero Decision allows.
func Of(d agentpolicy.Decision) Outcome {
	if out, ok := d.Outcome(Name); ok {
		if typed, ok := out.(Outcome); ok {
			return typed
		}
	}
	return Outcome{}
}
