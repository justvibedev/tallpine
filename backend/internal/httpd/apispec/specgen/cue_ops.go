package specgen

import (
	"net/http"

	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
)

// cueOperations declares the project cue surface: reusable quick actions a
// user defines per project and invokes through an agent session or normal shell terminal.
func cueOperations() []operation {
	return []operation{
		{
			method: http.MethodGet, path: "/api/v1/projects/{projectId}/cues", id: "listProjectCues", tag: "cues",
			summary:    "List a project's cues in name order",
			pathParams: []any{controllers.CueProjectIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.ListCuesResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/projects/{projectId}/cues", id: "createCue", tag: "cues",
			summary:    "Create a cue for a project",
			pathParams: []any{controllers.CueProjectIDParam{}},
			reqBody:    controllers.CueDefinitionRequest{},
			resps: []respUnit{
				{http.StatusCreated, controllers.CueEnvelope{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusRequestEntityTooLarge, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/cues/{cueId}", id: "getCue", tag: "cues",
			summary:    "Get a cue",
			pathParams: []any{controllers.CueIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.CueEnvelope{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPatch, path: "/api/v1/cues/{cueId}", id: "updateCue", tag: "cues",
			summary:    "Replace a cue's definition",
			pathParams: []any{controllers.CueIDParam{}},
			reqBody:    controllers.CueDefinitionRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.CueEnvelope{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusRequestEntityTooLarge, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodDelete, path: "/api/v1/cues/{cueId}", id: "deleteCue", tag: "cues",
			summary:    "Delete a cue",
			pathParams: []any{controllers.CueIDParam{}},
			resps: []respUnit{
				{http.StatusNoContent, nil},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/cues/{cueId}/invoke", id: "invokeCue", tag: "cues",
			summary:    "Dispatch an agent cue to a session or send a command cue to a scoped shell terminal",
			pathParams: []any{controllers.CueIDParam{}},
			reqBody:    controllers.InvokeCueRequest{}, optionalReqBody: true,
			resps: []respUnit{
				{http.StatusOK, controllers.InvokeCueResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusForbidden, envelope.APIError{}},
				{http.StatusRequestEntityTooLarge, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
	}
}
