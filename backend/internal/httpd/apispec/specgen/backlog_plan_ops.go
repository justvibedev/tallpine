package specgen

import (
	"net/http"

	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
)

func init() {
	for k, v := range map[string]string{
		"ApitypesPlanOptionDTO":           "PlanOption",
		"ApitypesPlanQuestionDTO":         "PlanQuestion",
		"ApitypesPlanAssumptionDTO":       "PlanAssumption",
		"ApitypesPlanAnswerDTO":           "PlanAnswer",
		"ApitypesPlanAnswersDTO":          "PlanAnswers",
		"ApitypesPlanPRDTO":               "PlanPR",
		"ControllersProjectPlanDTO":       "ProjectPlan",
		"ControllersProjectPlansResponse": "ProjectPlansResponse",
		"ControllersPlanEnvelope":         "PlanEnvelope",
		"ControllersPlanQuestionsRequest": "PlanQuestionsRequest",
		"ControllersPlanAnswersRequest":   "PlanAnswersRequest",
		"ControllersPlanDefaultsRequest":  "PlanDefaultsRequest",
		"ControllersPlanSubmitRequest":    "PlanSubmitRequest",
		"ControllersPlanApproveRequest":   "PlanApproveRequest",
	} {
		schemaNames[k] = v
	}
}

// backlogPlanOperations declares the plan routes of a backlog item (B205).
// Every operation answers 404 PLANNER_DISABLED while TALLPINE_PLANNER is off.
func backlogPlanOperations() []operation {
	item := []any{controllers.BacklogProjectIDParam{}, controllers.BacklogRefParam{}}
	errs := []respUnit{
		{http.StatusBadRequest, envelope.APIError{}},
		{http.StatusForbidden, envelope.APIError{}},
		{http.StatusNotFound, envelope.APIError{}},
		{http.StatusConflict, envelope.APIError{}},
		{http.StatusInternalServerError, envelope.APIError{}},
		{http.StatusNotImplemented, envelope.APIError{}},
	}
	with := func(ok respUnit) []respUnit { return append([]respUnit{ok}, errs...) }
	ok := with(respUnit{http.StatusOK, controllers.PlanEnvelope{}})
	base := "/api/v1/projects/{projectId}/backlog/{ref}/plan"
	return []operation{
		{method: http.MethodGet, path: "/api/v1/projects/{projectId}/plans", id: "listProjectPlans", tag: "backlog",
			summary: "List the project's plans that wait on someone (open or failed)", pathParams: []any{controllers.BacklogProjectIDParam{}},
			resps: with(respUnit{http.StatusOK, controllers.ProjectPlansResponse{}})},
		{method: http.MethodGet, path: base, id: "getBacklogItemPlan", tag: "backlog",
			summary: "Get an item's current plan", pathParams: item, resps: ok},
		{method: http.MethodPost, path: base, id: "startBacklogItemPlan", tag: "backlog",
			summary: "Start planning an item (Plan now); an open plan is returned as is", pathParams: item,
			resps: with(respUnit{http.StatusCreated, controllers.PlanEnvelope{}})},
		{method: http.MethodPost, path: base + "/replan", id: "replanBacklogItem", tag: "backlog",
			summary: "Supersede the plan and plan again", pathParams: item,
			resps: with(respUnit{http.StatusCreated, controllers.PlanEnvelope{}})},
		{method: http.MethodPost, path: base + "/questions", id: "submitBacklogPlanQuestions", tag: "backlog",
			summary: "Planner: submit at most 5 questions and 20 assumptions", pathParams: item, reqBody: controllers.PlanQuestionsRequest{}, resps: ok},
		{method: http.MethodPost, path: base + "/submit", id: "submitBacklogPlan", tag: "backlog",
			summary: "Planner: submit the plan, decisions table, PR split and review focus", pathParams: item, reqBody: controllers.PlanSubmitRequest{}, resps: ok},
		{method: http.MethodPost, path: base + "/answers", id: "answerBacklogPlan", tag: "backlog",
			summary: "Answer the questions and correct assumptions", pathParams: item, reqBody: controllers.PlanAnswersRequest{}, resps: ok},
		{method: http.MethodPost, path: base + "/defaults", id: "acceptBacklogPlanDefaults", tag: "backlog",
			summary: "Go with the recommended answers and every assumption", pathParams: item, reqBody: controllers.PlanDefaultsRequest{}, resps: ok},
		{method: http.MethodPost, path: base + "/approve", id: "approveBacklogPlan", tag: "backlog",
			summary: "Approve the submitted plan (an orchestrator needs the owner's message)", pathParams: item, reqBody: controllers.PlanApproveRequest{}, resps: ok},
		{method: http.MethodPost, path: base + "/build-anyway", id: "buildBacklogPlanAnyway", tag: "backlog",
			summary: "Build from the approved plan although it may be stale", pathParams: item, reqBody: controllers.PlanApproveRequest{}, resps: ok},
	}
}
