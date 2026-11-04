package daemon

import (
	"context"
	"fmt"
	"log/slog"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/plannerpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
	sessionmanager "tallpine.eu/tallpine/backend/internal/session_manager"
)

// newAgentPolicyEngine builds the agent policy engine over every registered
// rule. With no managed file, no TALLPINE_AGENT_* variable and untouched settings
// every rule is at its no-restriction default.
func newAgentPolicyEngine(user agentpolicy.UserStore) (*agentpolicy.Engine, error) {
	engine, err := agentpolicy.New(rules.All(), user)
	if err != nil {
		return nil, fmt.Errorf("agent policy: %w", err)
	}
	return engine, nil
}

// wireAgentPolicy hands the engine to the session manager, which computes one
// Decision per session and passes it to the enforcement seams, and reports the
// settings in force.
func wireAgentPolicy(engine *agentpolicy.Engine, sessMgr any, log *slog.Logger) {
	if wired, ok := sessMgr.(interface {
		SetAgentPolicy(sessionmanager.PolicyEngine)
	}); ok {
		wired.SetAgentPolicy(engine)
	}
	sessionmanager.SetPlannerLock(func() (domain.RoleOverride, bool) {
		return plannerpolicy.Of(engine.DecideStatic(context.Background())).ModelLock()
	})
	logAgentPolicy(engine, log)
}

// logAgentPolicy reports at startup every setting that is not at its default,
// with the layer that set it, and every warning. Names and values of the
// settings only: nothing here is a secret.
func logAgentPolicy(engine *agentpolicy.Engine, log *slog.Logger) {
	view := engine.Describe(context.Background(), "")
	for _, warning := range view.Warnings {
		log.Warn("agent policy: "+warning, "path", view.ManagedPath)
	}
	for _, rule := range view.Rules {
		for _, field := range rule.Fields {
			if field.Source == agentpolicy.SourceDefault {
				continue
			}
			log.Info("agent policy: setting active", "rule", rule.Name, "setting", field.Name, "value", string(field.Value), "source", field.Source, "locked", field.Locked)
		}
	}
}
