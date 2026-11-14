package domain

import "encoding/json"

// WorkflowDefaults are the app-wide workflow values a project inherits when it
// sets none of its own (B525). People write them; an agent token cannot.
type WorkflowDefaults struct {
	// AutoReview is the auto-review flag a project without its own value gives
	// each new worker.
	AutoReview bool `json:"autoReview"`
	// PlanningMode is the planning mode a project without its own value uses.
	PlanningMode PlanningMode `json:"planningMode"`
	// PlanAutoApprove is whether a project without its own value approves
	// plans without a person.
	PlanAutoApprove bool `json:"planAutoApprove"`
	// EvalWaiverClosed keeps the eval waiver (an orchestrator approving plans
	// without the owner) shut in every project. The migration of a user-layer
	// planner.autoApprove "off" sets it; it has no API or UI and is preserved
	// by every defaults write.
	EvalWaiverClosed bool `json:"evalWaiverClosed,omitempty"`
	// ReviewerHarness and ReviewerModel are the reviewer a project without its
	// own reviewers uses (B402). Empty harness means no app default: the
	// project falls back to its worker-derived reviewer.
	ReviewerHarness ReviewerHarness `json:"reviewerHarness,omitempty"`
	ReviewerModel   string          `json:"reviewerModel,omitempty"`
	// Worker* and Orchestrator* are the agent, model and effort a NEW project
	// starts with for that role (copied into its config when it is created;
	// existing projects are never touched). Empty means no app default. A model
	// and effort belong to their harness, so both are empty while it is.
	WorkerAgent        AgentHarness `json:"workerAgent,omitempty"`
	WorkerModel        string       `json:"workerModel,omitempty"`
	WorkerEffort       string       `json:"workerEffort,omitempty"`
	OrchestratorAgent  AgentHarness `json:"orchestratorAgent,omitempty"`
	OrchestratorModel  string       `json:"orchestratorModel,omitempty"`
	OrchestratorEffort string       `json:"orchestratorEffort,omitempty"`
}

// ApplyNewProjectRoles fills the worker and orchestrator agent, model and
// effort of a new project's config from the app defaults, only where the
// caller left the field empty. A caller that chose a different harness for a
// role keeps its own choice whole: the default model and effort belong to the
// default harness and would not apply to it.
func (d WorkflowDefaults) ApplyNewProjectRoles(c *ProjectConfig) {
	fillRoleDefault(&c.Worker, d.WorkerAgent, d.WorkerModel, d.WorkerEffort)
	fillRoleDefault(&c.Orchestrator, d.OrchestratorAgent, d.OrchestratorModel, d.OrchestratorEffort)
}

func fillRoleDefault(ro *RoleOverride, agent AgentHarness, model, effort string) {
	if agent == "" || (ro.Harness != "" && ro.Harness != agent) {
		return
	}
	if ro.Harness == "" {
		ro.Harness = agent
	}
	if ro.AgentConfig.Model == "" {
		ro.AgentConfig.Model = model
	}
	if ro.AgentConfig.Effort == "" {
		ro.AgentConfig.Effort = effort
	}
}

// ReviewerOverride is the app-wide default reviewer, or false when none is set.
func (d WorkflowDefaults) ReviewerOverride() (ReviewerHarness, AgentConfig, bool) {
	if d.ReviewerHarness == "" {
		return "", AgentConfig{}, false
	}
	return d.ReviewerHarness, AgentConfig{Model: d.ReviewerModel}, true
}

// EffectiveReviewer is the reviewer a worker gets: the project's own reviewer,
// else the app-wide default reviewer, else the project's worker-derived one.
func (c ProjectConfig) EffectiveReviewer(d WorkflowDefaults, worker AgentHarness) (ReviewerHarness, AgentConfig) {
	if len(c.Reviewers) > 0 {
		return c.Reviewers[0].Harness, c.Reviewers[0].AgentConfig
	}
	if h, cfg := c.InheritedReviewer(d); h != "" {
		return h, cfg
	}
	return c.ResolveReviewerHarness(worker), AgentConfig{}
}

// InheritedReviewer is the reviewer a project with no reviewers of its own
// follows: the app-wide default, else its worker-derived one. The harness is
// empty when neither exists.
func (c ProjectConfig) InheritedReviewer(d WorkflowDefaults) (ReviewerHarness, AgentConfig) {
	if h, cfg, ok := d.ReviewerOverride(); ok {
		return h, cfg
	}
	return c.DefaultWorkerReviewer()
}

// DefaultWorkflowDefaults are the built-in values: auto-review off, plans need
// a person's approval, planning mode auto.
func DefaultWorkflowDefaults() WorkflowDefaults {
	return WorkflowDefaults{
		PlanningMode:       PlanningAuto,
		WorkerAgent:        BuiltinWorkerAgent,
		WorkerModel:        BuiltinWorkerModel,
		WorkerEffort:       BuiltinRoleEffort,
		OrchestratorAgent:  BuiltinOrchestratorAgent,
		OrchestratorModel:  BuiltinOrchestratorModel,
		OrchestratorEffort: BuiltinRoleEffort,
	}
}

// Built-in role defaults a new project starts with while the owner has set
// none. An explicit value, including an explicit clear, always wins.
const (
	BuiltinWorkerAgent       = HarnessClaudeCode
	BuiltinWorkerModel       = "claude-sonnet-5-5"
	BuiltinOrchestratorAgent = HarnessClaudeCode
	BuiltinOrchestratorModel = "claude-opus-5-5"
	BuiltinRoleEffort        = "medium"
)

// UsesBuiltinRoleAgent reports whether the worker (orchestrator when false)
// default is the built-in one, which is skipped on a machine where that agent
// is not ready (an explicit choice is never skipped).
func (d WorkflowDefaults) UsesBuiltinRoleAgent(orchestrator bool) bool {
	if orchestrator {
		return d.OrchestratorAgent == BuiltinOrchestratorAgent
	}
	return d.WorkerAgent == BuiltinWorkerAgent
}

// EncodeStored is the JSON the store persists. A role field the owner emptied
// is written as an explicit "" so reading it back does not restore the
// built-in value (the json tags omit empty fields, which means "never set").
func (d WorkflowDefaults) EncodeStored() ([]byte, error) {
	raw, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	for key, builtin := range map[string]string{
		"workerAgent": string(BuiltinWorkerAgent), "workerModel": BuiltinWorkerModel, "workerEffort": BuiltinRoleEffort,
		"orchestratorAgent": string(BuiltinOrchestratorAgent), "orchestratorModel": BuiltinOrchestratorModel, "orchestratorEffort": BuiltinRoleEffort,
	} {
		if _, set := m[key]; !set && builtin != "" {
			m[key] = json.RawMessage(`""`)
		}
	}
	return json.Marshal(m)
}

// Normalized fills an empty or unknown planning mode with the default.
func (d WorkflowDefaults) Normalized() WorkflowDefaults {
	if d.PlanningMode == "" || !d.PlanningMode.Valid() {
		d.PlanningMode = PlanningAuto
	}
	return d
}

// EffectiveAutoReview is the project's own auto-review value, or the app
// default when the project sets none.
func (c ProjectConfig) EffectiveAutoReview(d WorkflowDefaults) bool {
	if c.AutoReview != nil {
		return *c.AutoReview
	}
	return d.AutoReview
}

// EffectivePlanningMode is the project's own planning mode, or the app default
// when the project sets none. An explicit "auto" stays the project's choice.
func (c ProjectConfig) EffectivePlanningMode(d WorkflowDefaults) PlanningMode {
	if c.PlanningMode != "" {
		return c.PlanningMode
	}
	return d.Normalized().PlanningMode
}

// EffectivePlanAutoApprove is the project's own plan auto-approve value, or the
// app default when the project sets none. It does not apply a managed lock.
func (c ProjectConfig) EffectivePlanAutoApprove(d WorkflowDefaults) bool {
	if c.PlanAutoApprove != nil {
		return *c.PlanAutoApprove
	}
	return d.PlanAutoApprove
}

// WithoutBuiltinRoleModels returns the config with every built-in default role
// model (and the effort set beside it) cleared, so the agent runs its own
// default model. changed is false when none was set. A model the owner chose
// is never equal to a built-in one, except by choosing the same ID.
func (c ProjectConfig) WithoutBuiltinRoleModels() (out ProjectConfig, changed bool) {
	out = c
	dropBuiltin := func(a *AgentConfig) {
		if a.Model == BuiltinWorkerModel || a.Model == BuiltinOrchestratorModel {
			a.Model, a.Effort = "", ""
			changed = true
		}
	}
	dropBuiltin(&out.AgentConfig)
	dropBuiltin(&out.Worker.AgentConfig)
	dropBuiltin(&out.Orchestrator.AgentConfig)
	dropBuiltin(&out.Planner.AgentConfig)
	return out, changed
}
