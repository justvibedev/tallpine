// Package accountpool is the rule that decides whether and how sessions are
// spread across several signed-in accounts of one harness: where a new session
// starts (placement) and whether a session whose account hit a usage limit
// moves to another account with headroom (failover). Both are off by default,
// which is today's behaviour: the default account, and wait for the reset.
//
// Unlike the security rules, this one restricts nothing; it is a rule so that
// an administrator can switch the pool on through the same managed layer, and
// so the environment and Settings share one precedence. See docs/agent-policy.md.
package accountpool

import (
	"encoding/json"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
)

// Name is the rule's key in settings documents.
const Name = "accountPool"

// Field names.
const (
	FieldPlacement = "placement"
	FieldFailover  = "failover"
)

var placements = []string{
	string(domain.AccountPlacementDefault),
	string(domain.AccountPlacementRandom),
	string(domain.AccountPlacementMostHeadroom),
	string(domain.AccountPlacementLeastUsed),
}

// Rule implements agentpolicy.Rule.
type Rule struct{}

// Name implements agentpolicy.Rule.
func (Rule) Name() string { return Name }

// Schema implements agentpolicy.Rule.
func (Rule) Schema() agentpolicy.Schema {
	return agentpolicy.Schema{
		TitleKey: "settings.accountPool.title",
		Fields: []agentpolicy.Field{
			{
				Name:           FieldPlacement,
				Kind:           agentpolicy.KindEnum,
				Enum:           placements,
				Default:        json.RawMessage(`"default"`),
				EnvAliases:     []string{"TALLPINE_ACCOUNT_PLACEMENT"},
				LabelKey:       "settings.accountPool.placement.label",
				DescriptionKey: "settings.accountPool.placement.description",
				OptionLabelKeys: map[string]string{
					string(domain.AccountPlacementDefault):      "settings.accountPool.placement.default",
					string(domain.AccountPlacementRandom):       "settings.accountPool.placement.random",
					string(domain.AccountPlacementMostHeadroom): "settings.accountPool.placement.mostHeadroom",
					string(domain.AccountPlacementLeastUsed):    "settings.accountPool.placement.leastUsed",
				},
				ProjectOverride: agentpolicy.ProjectNever,
				// A typo must not switch spreading on: it is ignored and reported.
				OnInvalid: agentpolicy.InvalidIgnore,
			},
			{
				Name:            FieldFailover,
				Kind:            agentpolicy.KindBool,
				Default:         json.RawMessage(`false`),
				EnvAliases:      []string{"TALLPINE_ACCOUNT_FAILOVER"},
				LabelKey:        "settings.accountPool.failover.label",
				DescriptionKey:  "settings.accountPool.failover.description",
				ProjectOverride: agentpolicy.ProjectNever,
			},
		},
	}
}

// Validate implements agentpolicy.Rule.
func (Rule) Validate(agentpolicy.Settings) error { return nil }

// Decide implements agentpolicy.Rule.
func (Rule) Decide(_ agentpolicy.Input, s agentpolicy.Settings) agentpolicy.Outcome {
	placement, _ := domain.ParseAccountPlacement(s.String(FieldPlacement))
	return Outcome{placement: placement, failover: s.Bool(FieldFailover)}
}
