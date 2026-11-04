package settingsreg

import (
	"encoding/json"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
)

// policyKinds maps agent-policy field kinds onto registry kinds.
var policyKinds = map[agentpolicy.FieldKind]Kind{
	agentpolicy.KindEnum:       KindEnum,
	agentpolicy.KindBool:       KindBool,
	agentpolicy.KindStringList: KindStringList,
	agentpolicy.KindInt:        KindInt,
	agentpolicy.KindString:     KindString,
}

// policySettings declares every field of every agent-policy rule as
// policy.<rule>.<field>. The rule schema stays the definition the policy engine
// reads; this mirrors it so the registry lists every setting. Agents never use
// /settings/agent-policy; an orchestrator may tighten a field of its own
// project through PATCH /settings/values (policyWriters).
func policySettings(all []agentpolicy.Rule) []Setting {
	var out []Setting
	for _, r := range all {
		for _, f := range r.Schema().Fields {
			kind, ok := policyKinds[f.Kind]
			if !ok {
				kind = Kind(f.Kind) // rejected by check, so a new kind is noticed
			}
			out = append(out, Setting{
				Key:         Key("policy." + r.Name() + "." + f.Name),
				Scope:       ScopePolicy,
				Kind:        kind,
				Default:     f.Default,
				Enum:        f.Enum,
				Description: "Agent policy " + r.Name() + ": " + f.Name + " (see docs/agent-policy.md).",
				Env:         f.EnvNames(r.Name()),
				MDMLockable: true,
				Writers:     policyWriters(f),
				Validate:    func(raw json.RawMessage) error { _, err := f.Parse(raw); return err },
			})
		}
	}
	return out
}

// policyWriters lets an orchestrator write a policy field it can only make
// stricter: an ordered enum its project may override. settingswrite refuses
// an agent's value that loosens it (B202 Q2).
func policyWriters(f agentpolicy.Field) []Actor {
	if f.Kind == agentpolicy.KindEnum && (f.ProjectOverride == agentpolicy.ProjectTightenOnly || f.ProjectOverride == agentpolicy.ProjectAny) {
		return []Actor{ActorUser, ActorOrchestrator}
	}
	if f.Kind == agentpolicy.KindBool && f.ProjectOverride == agentpolicy.ProjectAny && f.OnInvalid == agentpolicy.InvalidStrictest {
		// Security mode: an orchestrator may turn it on, never off (settingswrite).
		return []Actor{ActorUser, ActorOrchestrator}
	}
	return []Actor{ActorUser}
}
