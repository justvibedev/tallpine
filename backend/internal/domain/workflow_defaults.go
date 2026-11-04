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
