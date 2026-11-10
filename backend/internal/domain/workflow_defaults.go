package domain

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
	return WorkflowDefaults{PlanningMode: PlanningAuto}
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
