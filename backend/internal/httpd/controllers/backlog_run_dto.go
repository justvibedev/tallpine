package controllers

// BacklogRunRefParam is the {runRef} path parameter: R3 or 3.
type BacklogRunRefParam struct {
	RunRef string `path:"runRef" description:"Run ref (R3 or 3)."`
}

// BacklogRunListQuery filters GET /projects/{projectId}/backlog/runs.
type BacklogRunListQuery struct {
	Open  string `query:"open,omitempty" enum:"1" description:"Only the run that has not finished."`
	Limit string `query:"limit,omitempty" description:"Maximum runs (default 20, max 100)."`
}

// BacklogRunItemRefParam is the {ref} path parameter of a run's item route.
type BacklogRunItemRefParam struct {
	Ref string `path:"ref" description:"Item ref (B12 or 12)."`
}

// BacklogRunPlanItemRequest is one planned item.
type BacklogRunPlanItemRequest struct {
	Ref   string `json:"ref" description:"Item ref."`
	Model string `json:"model" description:"Model the item's worker runs on."`
}

// BacklogRunPlanRequest replaces a run's plan: items in a lane run in order, lanes run in parallel.
type BacklogRunPlanRequest struct {
	Lanes [][]BacklogRunPlanItemRequest `json:"lanes"`
}

// BacklogRunFinishRequest ends the run with the orchestrator's narrative.
type BacklogRunFinishRequest struct {
	Summary string `json:"summary"`
}

// BacklogUsageEnvelope is the body of GET /projects/{projectId}/backlog/{ref}/usage.
type BacklogUsageEnvelope struct {
	Usage BacklogUsageResponse `json:"usage"`
}

// BacklogRunEnvelope wraps one run.
type BacklogRunEnvelope struct {
	Run BacklogRunResponse `json:"run"`
}

// ListBacklogRunsResponse lists runs without their plans.
type ListBacklogRunsResponse struct {
	Runs []BacklogRunResponse `json:"runs"`
}
