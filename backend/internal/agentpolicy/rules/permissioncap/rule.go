// Package permissioncap is the rule that caps how much freedom agents get: it
// forbids bypass / full access (level auto) or anything above accept-edits
// (level accept_edits). Level none, the default, changes nothing.
package permissioncap

import (
	"encoding/json"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
)

// Name is the rule's key in settings documents.
const Name = "permissionCap"

// FieldMax is the cap level field.
const FieldMax = "max"

// Level is the most permissive mode agents may run in.
type Level string

// Levels in ascending strictness. None is today's behaviour.
const (
	None        Level = "none"
	Auto        Level = "auto"
	AcceptEdits Level = "accept_edits"
)

var levels = []string{string(None), string(Auto), string(AcceptEdits)}

// Rule implements agentpolicy.Rule.
type Rule struct{}

// Name implements agentpolicy.Rule.
func (Rule) Name() string { return Name }

// Schema implements agentpolicy.Rule.
func (Rule) Schema() agentpolicy.Schema {
	return agentpolicy.Schema{
		TitleKey: "settings.agentPolicy.title",
		Fields: []agentpolicy.Field{{
			Name:           FieldMax,
			Kind:           agentpolicy.KindEnum,
			Enum:           levels,
			Default:        json.RawMessage(`"none"`),
			EnvAliases:     []string{"TALLPINE_AGENT_MAX_PERMISSION"},
			LegacyKey:      "maxPermission",
			LabelKey:       "settings.agentPolicy.cap.label",
			DescriptionKey: "settings.agentPolicy.cap.description",
			OptionLabelKeys: map[string]string{
				string(None):        "settings.agentPolicy.cap.none",
				string(Auto):        "settings.agentPolicy.cap.auto",
				string(AcceptEdits): "settings.agentPolicy.cap.acceptEdits",
			},
			ProjectOverride: agentpolicy.ProjectTightenOnly,
			OnInvalid:       agentpolicy.InvalidStrictest,
		}},
	}
}

// Validate implements agentpolicy.Rule. The schema covers everything.
func (Rule) Validate(agentpolicy.Settings) error { return nil }

// Decide implements agentpolicy.Rule.
func (Rule) Decide(_ agentpolicy.Input, s agentpolicy.Settings) agentpolicy.Outcome {
	return Outcome{level: parse(s.String(FieldMax))}
}

func parse(v string) Level {
	for _, l := range levels {
		if v == l {
			return Level(v)
		}
	}
	return None
}

func (l Level) rank() int {
	for i, v := range levels {
		if string(l) == v {
			return i
		}
	}
	return 0
}

func stricter(a, b Level) Level {
	if b.rank() > a.rank() {
		return b
	}
	return a
}
