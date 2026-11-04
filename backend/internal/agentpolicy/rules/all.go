// Package rules lists the agent policy rules. Adding a rule is one package
// under this directory plus one line here; the engine, API, CLI and Settings UI
// pick it up from its schema.
package rules

import (
	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/accountpool"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/agentsettings"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/crossprojectorchestration"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/devmode"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/envfilter"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/githubcreds"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/orchestratorapprovals"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/orchestratorlanes"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/plannerpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/protectstate"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/repoagentconfig"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/securitymode"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/sessionretention"
)

// All returns every rule in display order.
func All() []agentpolicy.Rule {
	return []agentpolicy.Rule{
		permissioncap.Rule{},
		githubcreds.Rule{},
		envfilter.Rule{},
		protectstate.Rule{},
		securitymode.Rule{},
		accountpool.Rule{},
		orchestratorapprovals.Rule{},
		crossprojectorchestration.Rule{},
		orchestratorlanes.Rule{},
		devmode.Rule{},
		repoagentconfig.Rule{},
		sessionretention.Rule{},
		agentsettings.Rule{},
		plannerpolicy.Rule{},
	}
}
