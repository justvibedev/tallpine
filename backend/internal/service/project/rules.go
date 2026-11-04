package project

import (
	"context"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
)

// SetRulesInput is the body of PUT /api/v1/projects/{id}/rules: the project's
// two standing-instruction text fields and nothing else. A nil field is left
// unchanged; an empty string clears it.
type SetRulesInput struct {
	AgentRules        *string `json:"agentRules,omitempty"`
	OrchestratorRules *string `json:"orchestratorRules,omitempty"`
	// OrchestratorAutoCompactWindow is orchestrator.autoCompactWindow: the
	// context size at which the project's orchestrators compact (B193).
	OrchestratorAutoCompactWindow *string `json:"orchestratorAutoCompactWindow,omitempty"`
	// OrchestratorIdleCompactAt is orchestrator.idleCompactAt: the context size
	// above which an idle orchestrator compacts between tasks (B204).
	OrchestratorIdleCompactAt *string `json:"orchestratorIdleCompactAt,omitempty"`
	// OrchestratorResearchCompactWindow is orchestrator.researchCompactWindow
	// (B413): the compaction window of an orchestrator in research mode.
	OrchestratorResearchCompactWindow *string `json:"orchestratorResearchCompactWindow,omitempty"`
	// Actor names who made the change for the audit log ("user" or the agent
	// session). It is set by the controller, never decoded from the body.
	Actor string `json:"-"`
}

// SetRules updates only the project's agentRules and orchestratorRules. Every
// other config field is kept as stored, so this is the one config write a
// scoped orchestrator token may make on its own project. Each changed field is
// written to the daemon log with the actor and the old and new lengths.
func (m *Service) SetRules(ctx context.Context, id domain.ProjectID, in SetRulesInput) (Project, error) {
	if err := validateProjectID(id); err != nil {
		return Project{}, err
	}
	if in.AgentRules == nil && in.OrchestratorRules == nil && in.OrchestratorAutoCompactWindow == nil && in.OrchestratorIdleCompactAt == nil && in.OrchestratorResearchCompactWindow == nil {
		return Project{}, apierr.Invalid("INVALID_PROJECT_RULES", "Set agentRules, orchestratorRules, orchestratorAutoCompactWindow, orchestratorIdleCompactAt, orchestratorResearchCompactWindow or a combination", nil)
	}
	row, ok, err := m.store.GetProject(ctx, string(id))
	if err != nil {
		return Project{}, projectStoreError(err, "PROJECT_LOAD_FAILED", "Failed to load project")
	}
	if !ok || !row.ArchivedAt.IsZero() {
		return Project{}, apierr.NotFound("PROJECT_NOT_FOUND", "Unknown project")
	}
	before := row.Config
	next := row.Config
	if in.AgentRules != nil {
		next.AgentRules = *in.AgentRules
	}
	if in.OrchestratorRules != nil {
		next.OrchestratorRules = *in.OrchestratorRules
	}
	if in.OrchestratorAutoCompactWindow != nil {
		next.Orchestrator.AutoCompactWindow = *in.OrchestratorAutoCompactWindow
	}
	if in.OrchestratorIdleCompactAt != nil {
		next.Orchestrator.IdleCompactAt = *in.OrchestratorIdleCompactAt
	}
	if in.OrchestratorResearchCompactWindow != nil {
		next.Orchestrator.ResearchCompactWindow = *in.OrchestratorResearchCompactWindow
	}
	if err := next.Validate(); err != nil {
		return Project{}, apierr.Invalid("INVALID_PROJECT_CONFIG", err.Error(), nil)
	}
	if err := next.ValidateAgentRulesCap(before.AgentRules); err != nil {
		return Project{}, apierr.Invalid("INVALID_PROJECT_CONFIG", err.Error(), nil)
	}
	if err := validateOrchestratorRulesCap(next.OrchestratorRules, before.OrchestratorRules); err != nil {
		return Project{}, apierr.Invalid("INVALID_PROJECT_CONFIG", err.Error(), nil)
	}
	row.Config = next
	if err := m.store.UpsertProject(ctx, row); err != nil {
		return Project{}, projectStoreError(err, "PROJECT_CONFIG_UPDATE_FAILED", "Failed to update project rules")
	}
	actor := in.Actor
	if actor == "" {
		actor = "user"
	}
	m.recordConfig(ctx, id, before, next)
	m.logRulesChange(id, actor, "agentRules", in.AgentRules, before.AgentRules)
	m.logRulesChange(id, actor, "orchestratorRules", in.OrchestratorRules, before.OrchestratorRules)
	if in.OrchestratorAutoCompactWindow != nil {
		m.logger.Info("project orchestrator compaction window changed", "project", string(id), "actor", actor,
			"old", before.Orchestrator.AutoCompactWindow, "new", *in.OrchestratorAutoCompactWindow)
	}
	if in.OrchestratorIdleCompactAt != nil {
		m.logger.Info("project orchestrator idle compaction threshold changed", "project", string(id), "actor", actor,
			"old", before.Orchestrator.IdleCompactAt, "new", *in.OrchestratorIdleCompactAt)
	}
	if in.OrchestratorResearchCompactWindow != nil {
		m.logger.Info("project orchestrator research window changed", "project", string(id), "actor", actor,
			"old", before.Orchestrator.ResearchCompactWindow, "new", *in.OrchestratorResearchCompactWindow)
	}
	return m.projectFromRow(ctx, row), nil
}

func (m *Service) logRulesChange(id domain.ProjectID, actor, field string, value *string, old string) {
	if value == nil {
		return
	}
	m.logger.Info("project rules changed",
		"project", string(id), "actor", actor, "field", field,
		"oldLength", len(old), "newLength", len(*value))
}

// MaxOrchestratorRulesBytes caps orchestratorRules set through the rules route,
// which an orchestrator token may call, so one session cannot inflate every
// later orchestrator prompt of the project. It mirrors MaxAgentRulesBytes.
const MaxOrchestratorRulesBytes = domain.MaxAgentRulesBytes

// validateOrchestratorRulesCap refuses orchestratorRules over the cap unless
// they are no longer than the stored value, so a legacy oversized value can
// still be shortened.
func validateOrchestratorRulesCap(next, previous string) error {
	if len(next) <= MaxOrchestratorRulesBytes || len(next) <= len(previous) {
		return nil
	}
	return fmt.Errorf("orchestratorRules is %d bytes, over the %d byte limit", len(next), MaxOrchestratorRulesBytes)
}
