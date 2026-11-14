package project

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// withNewProjectRoleDefaults fills the worker and orchestrator agent, model and
// effort of a project being created from the app defaults, for the fields its
// creator left empty. It runs once at creation (never for an existing project)
// and every creation path (CLI, desktop, an orchestrator's cross-project add,
// clone) goes through Add. An unreadable store or a default that would make
// the config invalid leaves the creator's config as it was: a project is never
// refused over an optional default.
func (m *Service) withNewProjectRoleDefaults(ctx context.Context, cfg domain.ProjectConfig) domain.ProjectConfig {
	app, err := m.appWorkflowDefaults(ctx)
	if err != nil {
		m.logger.Warn("project: read role defaults for a new project", "error", err)
		return cfg
	}
	// The built-in default agent is only a preference: where it is known not
	// to be installed or signed in, the role keeps no default rather than
	// creating a project that cannot start. An explicit choice is applied as is.
	if m.agentNotReady != nil && (app.UsesBuiltinRoleAgent(false) || app.UsesBuiltinRoleAgent(true)) {
		if m.agentNotReady(ctx, domain.BuiltinWorkerAgent) {
			if app.UsesBuiltinRoleAgent(false) {
				app.WorkerAgent, app.WorkerModel, app.WorkerEffort = "", "", ""
			}
			if app.UsesBuiltinRoleAgent(true) {
				app.OrchestratorAgent, app.OrchestratorModel, app.OrchestratorEffort = "", "", ""
			}
		}
	}
	next := cfg
	app.ApplyNewProjectRoles(&next)
	if err := next.Validate(); err != nil {
		m.logger.Warn("project: app role defaults do not fit a new project; skipped", "error", err)
		return cfg
	}
	return next
}
