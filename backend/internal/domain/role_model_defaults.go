package domain

import "strings"

// DefaultRole names a role with an app-wide default agent, model and effort
// (B847): the orchestrator, the worker and the reviewer.
type DefaultRole string

// The roles with an app-wide default.
const (
	DefaultRoleOrchestrator DefaultRole = "orchestrator"
	DefaultRoleWorker       DefaultRole = "worker"
	DefaultRoleReviewer     DefaultRole = "reviewer"
)

// DefaultRoles lists every role with an app-wide default, in display order.
var DefaultRoles = []DefaultRole{DefaultRoleOrchestrator, DefaultRoleWorker, DefaultRoleReviewer}

// Valid reports whether r is one of DefaultRoles.
func (r DefaultRole) Valid() bool {
	return r == DefaultRoleOrchestrator || r == DefaultRoleWorker || r == DefaultRoleReviewer
}

// DefaultRoleOf is the app default a session inherits: an orchestrator and a
// planner (which follows the orchestrator) take the orchestrator's, any other
// session the worker's.
func DefaultRoleOf(kind SessionKind, role SessionRole) DefaultRole {
	if kind == KindOrchestrator || role == SessionRolePlanner {
		return DefaultRoleOrchestrator
	}
	return DefaultRoleWorker
}

// RoleModelDefault is one role's app-wide agent, model and effort.
type RoleModelDefault struct {
	Agent  AgentHarness
	Model  string
	Effort string
}

// RoleModelDefault returns the app-wide default of role.
func (d WorkflowDefaults) RoleModelDefault(role DefaultRole) RoleModelDefault {
	switch role {
	case DefaultRoleOrchestrator:
		return RoleModelDefault{Agent: d.OrchestratorAgent, Model: d.OrchestratorModel, Effort: d.OrchestratorEffort}
	case DefaultRoleWorker:
		return RoleModelDefault{Agent: d.WorkerAgent, Model: d.WorkerModel, Effort: d.WorkerEffort}
	case DefaultRoleReviewer:
		return RoleModelDefault{Agent: AgentHarness(d.ReviewerHarness), Model: d.ReviewerModel, Effort: d.ReviewerEffort}
	}
	return RoleModelDefault{}
}

// Fill gives cfg the default's model when it names none and the session runs
// on the default's agent (a model belongs to its agent). The default's effort
// comes with that model unless cfg sets its own; a project that names its own
// model keeps its own effort too, since an effort level belongs to the model.
func (r RoleModelDefault) Fill(launch AgentHarness, cfg AgentConfig) AgentConfig {
	if r.Agent == "" || launch != r.Agent || strings.TrimSpace(cfg.Model) != "" || r.Model == "" {
		return cfg
	}
	cfg.Model = r.Model
	if cfg.Effort == "" {
		cfg.Effort = r.Effort
	}
	return cfg
}

// RoleAgentConfig merges a role override's agent config over the project base
// for a session launching on harness ("" when not yet known).
//
// Model/Mode are inherited only when the launch harness matches the role's
// configured harness; otherwise they were tuned for a different agent and
// would leak a provider-specific alias onto the wrong harness. An empty role
// harness means "not pinned" and always matches. Permissions is
// harness-neutral and always inherited.
//
// The project base was tuned for the harness the role resolves to without an
// explicit agent: its pinned harness, else defaultAgent. When that harness is
// one this build no longer ships, or differs from the launch harness, the base
// Model/Effort/Mode are dropped and Permissions kept.
func (c ProjectConfig) RoleAgentConfig(harness AgentHarness, role RoleOverride, defaultAgent AgentHarness) AgentConfig {
	merged := c.AgentConfig
	baseHarness := role.Harness
	if baseHarness == "" {
		baseHarness = defaultAgent
	}
	if role.Harness.IsUnsupported() || (harness != "" && baseHarness != harness) {
		merged.Model, merged.Effort, merged.Mode = "", "", ""
	}
	override := role.AgentConfig
	harnessMatches := role.Harness == "" || role.Harness == harness
	if harnessMatches && override.Model != "" {
		merged.Model = override.Model
	}
	if harnessMatches && override.Effort != "" {
		merged.Effort = override.Effort
	}
	if harnessMatches && override.Mode != "" {
		merged.Mode = override.Mode
	}
	if override.Permissions != "" {
		merged.Permissions = override.Permissions
	}
	return merged
}

// WithoutBuiltinRoleModels is ProjectConfig.WithoutBuiltinRoleModels for the
// app defaults: every role model equal to a built-in default model is cleared
// with its effort, so an inheriting session runs the agent's own model.
func (d WorkflowDefaults) WithoutBuiltinRoleModels() (out WorkflowDefaults, changed bool) {
	out = d
	for _, pair := range [][2]*string{{&out.OrchestratorModel, &out.OrchestratorEffort}, {&out.WorkerModel, &out.WorkerEffort}, {&out.ReviewerModel, &out.ReviewerEffort}} {
		if *pair[0] == BuiltinWorkerModel || *pair[0] == BuiltinOrchestratorModel {
			*pair[0], *pair[1] = "", ""
			changed = true
		}
	}
	return out, changed
}
