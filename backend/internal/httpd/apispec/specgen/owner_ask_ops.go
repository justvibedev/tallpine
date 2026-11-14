package specgen

import (
	"net/http"

	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
)

func init() {
	for k, v := range map[string]string{
		"ControllersOwnerAskView":            "OwnerAsk",
		"ControllersOwnerAskAnswer":          "OwnerAskAnswer",
		"ControllersOwnerAsksResponse":       "OwnerAsksResponse",
		"ControllersOwnerAskEnvelope":        "OwnerAskEnvelope",
		"ControllersOwnerAskCreateRequest":   "OwnerAskCreateRequest",
		"ControllersOwnerAskAnswerRequest":   "OwnerAskAnswerRequest",
		"ControllersOwnerAskWithdrawRequest": "OwnerAskWithdrawRequest",
	} {
		schemaNames[k] = v
	}
}

// ownerAskOperations declares the tracked owner-ask routes (B698).
func ownerAskOperations() []operation {
	project := []any{controllers.BacklogProjectIDParam{}}
	ask := append(append([]any{}, project...), controllers.OwnerAskRefParam{})
	errs := []respUnit{
		{http.StatusBadRequest, envelope.APIError{}},
		{http.StatusForbidden, envelope.APIError{}},
		{http.StatusNotFound, envelope.APIError{}},
		{http.StatusConflict, envelope.APIError{}},
		{http.StatusInternalServerError, envelope.APIError{}},
		{http.StatusNotImplemented, envelope.APIError{}},
	}
	with := func(ok respUnit) []respUnit { return append([]respUnit{ok}, errs...) }
	one := with(respUnit{http.StatusOK, controllers.OwnerAskEnvelope{}})
	return []operation{
		{method: http.MethodGet, path: "/api/v1/projects/{projectId}/asks", id: "listProjectAsks", tag: "backlog",
			summary: "List the project's owner asks (open ones by default)", pathParams: append(append([]any{}, project...), controllers.OwnerAskListQuery{}),
			resps: with(respUnit{http.StatusOK, controllers.OwnerAsksResponse{}})},
		{method: http.MethodPost, path: "/api/v1/projects/{projectId}/asks", id: "createProjectAsk", tag: "backlog",
			summary: "Orchestrator: ask the owner a question", pathParams: project, reqBody: controllers.OwnerAskCreateRequest{},
			resps: with(respUnit{http.StatusCreated, controllers.OwnerAskEnvelope{}})},
		{method: http.MethodPost, path: "/api/v1/projects/{projectId}/asks/{ref}/answer", id: "answerProjectAsk", tag: "backlog",
			summary: "Answer an open ask (an orchestrator needs the owner's message)", pathParams: ask, reqBody: controllers.OwnerAskAnswerRequest{}, resps: one},
		{method: http.MethodPost, path: "/api/v1/projects/{projectId}/asks/{ref}/withdraw", id: "withdrawProjectAsk", tag: "backlog",
			summary: "Withdraw an open ask", pathParams: ask, reqBody: controllers.OwnerAskWithdrawRequest{}, resps: one},
	}
}
