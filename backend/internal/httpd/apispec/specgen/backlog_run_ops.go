package specgen

import (
	"net/http"

	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
)

func init() {
	for k, v := range map[string]string{
		"ControllersBacklogRunRefParam":        "BacklogRunRefParam",
		"ControllersBacklogRunListQuery":       "BacklogRunListQuery",
		"ControllersBacklogRunItemRefParam":    "BacklogRunItemRefParam",
		"ControllersBacklogRunPlanItemRequest": "BacklogRunPlanItemRequest",
		"ControllersBacklogRunPlanRequest":     "BacklogRunPlanRequest",
		"ControllersBacklogRunFinishRequest":   "BacklogRunFinishRequest",
		"ControllersBacklogUsageEnvelope":      "BacklogUsageEnvelope",
		"ControllersBacklogRunEnvelope":        "BacklogRunEnvelope",
		"ControllersListBacklogRunsResponse":   "ListBacklogRunsResponse",
	} {
		schemaNames[k] = v
	}
}

// backlogRunOperations declares backlog run mode ("finish the backlog"): a
// durable run, the orchestrator's plan and per-item progress, and cost/time per
// item. Every operation answers 404 BACKLOG_RUN_DISABLED when TALLPINE_BACKLOG_RUN=off.
func backlogRunOperations() []operation {
	project := []any{controllers.BacklogProjectIDParam{}}
	run := []any{controllers.BacklogProjectIDParam{}, controllers.BacklogRunRefParam{}}
	runItem := []any{controllers.BacklogProjectIDParam{}, controllers.BacklogRunRefParam{}, controllers.BacklogRunItemRefParam{}}
	errs := []respUnit{
		{http.StatusBadRequest, envelope.APIError{}},
		{http.StatusForbidden, envelope.APIError{}},
		{http.StatusNotFound, envelope.APIError{}},
		{http.StatusConflict, envelope.APIError{}},
		{http.StatusInternalServerError, envelope.APIError{}},
		{http.StatusNotImplemented, envelope.APIError{}},
	}
	with := func(ok respUnit) []respUnit { return append([]respUnit{ok}, errs...) }
	runOK := with(respUnit{http.StatusOK, controllers.BacklogRunEnvelope{}})
	base := "/api/v1/projects/{projectId}/backlog-runs"
	return []operation{
		{method: http.MethodGet, path: base, id: "listBacklogRuns", tag: "backlog",
			summary: "List a project's backlog runs, newest first", pathParams: []any{controllers.BacklogProjectIDParam{}, controllers.BacklogRunListQuery{}},
			resps: with(respUnit{http.StatusOK, controllers.ListBacklogRunsResponse{}})},
		{method: http.MethodPost, path: base, id: "startBacklogRun", tag: "backlog",
			summary: "Start a run (an owner request also messages the orchestrator once)", pathParams: project, reqBody: controllers.StartBacklogRunRequest{},
			resps: with(respUnit{http.StatusCreated, controllers.BacklogRunEnvelope{}})},
		{method: http.MethodGet, path: base + "/{runRef}", id: "getBacklogRun", tag: "backlog",
			summary: "Get a run with its plan, per-item status, cost and elapsed time", pathParams: run, resps: runOK},
		{method: http.MethodPut, path: base + "/{runRef}/plan", id: "saveBacklogRunPlan", tag: "backlog",
			summary: "Save the orchestrator's plan (lanes of items with a model each)", pathParams: run, reqBody: controllers.BacklogRunPlanRequest{}, resps: runOK},
		{method: http.MethodPost, path: base + "/{runRef}/pause", id: "pauseBacklogRun", tag: "backlog",
			summary: "Pause a running run", pathParams: run, reqBody: controllers.BacklogRunPauseRequest{}, resps: runOK},
		{method: http.MethodPost, path: base + "/{runRef}/resume", id: "resumeBacklogRun", tag: "backlog",
			summary: "Resume a paused run", pathParams: run, resps: runOK},
		{method: http.MethodPost, path: base + "/{runRef}/stop", id: "stopBacklogRun", tag: "backlog",
			summary: "Finish the work in flight and start nothing new", pathParams: run, resps: runOK},
		{method: http.MethodPost, path: base + "/{runRef}/finish", id: "finishBacklogRun", tag: "backlog",
			summary: "End the run with the orchestrator's summary", pathParams: run, reqBody: controllers.BacklogRunFinishRequest{}, resps: runOK},
		{method: http.MethodPost, path: base + "/{runRef}/items/{ref}", id: "setBacklogRunItem", tag: "backlog",
			summary: "Record one planned item's progress", pathParams: runItem, reqBody: controllers.BacklogRunItemRequest{}, resps: runOK},
		{method: http.MethodGet, path: "/api/v1/projects/{projectId}/backlog/{ref}/usage", id: "getBacklogItemUsage", tag: "backlog",
			summary:    "Cost and elapsed time of an item, summed over its linked sessions",
			pathParams: []any{controllers.BacklogProjectIDParam{}, controllers.BacklogRefParam{}},
			resps:      with(respUnit{http.StatusOK, controllers.BacklogUsageEnvelope{}})},
	}
}
