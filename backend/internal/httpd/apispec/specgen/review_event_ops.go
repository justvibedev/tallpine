package specgen

import (
	"net/http"

	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
)

func reviewOperations() []operation {
	return []operation{
		{
			method: http.MethodGet, path: "/api/v1/sessions/{sessionId}/reviews", id: "listReviews", tag: "reviews",
			summary:    "List a worker's code-review runs",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.ListReviewsResponse{}},
				{http.StatusUnprocessableEntity, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/reviews/trigger", id: "triggerReview", tag: "reviews",
			summary:    "Trigger a code review of a worker's PR",
			pathParams: []any{controllers.SessionIDParam{}},
			// Optional: an empty body runs under the project's configured reviewer.
			reqBody:         controllers.TriggerReviewRequest{},
			optionalReqBody: true,
			resps: []respUnit{
				{http.StatusOK, controllers.TriggerReviewResponse{}},
				{http.StatusCreated, controllers.TriggerReviewResponse{}},
				{http.StatusUnprocessableEntity, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/reviews/comments/resolve", id: "resolveReviewComment", tag: "reviews",
			summary:    "Resolve an external review comment thread",
			pathParams: []any{controllers.SessionIDParam{}},
			reqBody:    controllers.ResolveReviewCommentRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.ResolveReviewCommentResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusUnprocessableEntity, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/reviews/rerequest", id: "requestRereview", tag: "reviews",
			summary:    "Ask an external reviewer to re-review a worker's PR",
			pathParams: []any{controllers.SessionIDParam{}},
			reqBody:    controllers.RequestRereviewRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.RequestRereviewResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusUnprocessableEntity, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/reviews/cancel", id: "cancelReview", tag: "reviews",
			summary:    "Cancel a running code review",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.CancelReviewResponse{}},
				{http.StatusUnprocessableEntity, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/reviews/kill", id: "killReviewSession", tag: "reviews",
			summary:    "Archive a worker's reviewer and hide its surface while retaining history",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.KillReviewResponse{}},
				{http.StatusUnprocessableEntity, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/reviews/restore", id: "restoreReviewSession", tag: "reviews",
			summary:    "Restore a worker's reviewer terminal session",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.RestoreReviewResponse{}},
				{http.StatusUnprocessableEntity, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/reviews/switch", id: "switchReviewSession", tag: "reviews",
			summary:    "Switch a worker's reviewer harness",
			pathParams: []any{controllers.SessionIDParam{}},
			reqBody:    controllers.SetSessionReviewerRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.ListReviewsResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusUnprocessableEntity, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/reviews/submit", id: "submitReview", tag: "reviews",
			summary:    "Record a reviewer's result for a worker's PR",
			pathParams: []any{controllers.SessionIDParam{}},
			reqBody:    controllers.SubmitReviewInput{},
			resps: []respUnit{
				{http.StatusOK, controllers.ReviewRunResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusUnprocessableEntity, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
	}
}

type eventsQuery struct {
	After *string `query:"after,omitempty" description:"Replay events after a non-negative sequence, or use latest to start at the current head. Last-Event-ID overrides latest on reconnect."`
}

func eventOperations() []operation {
	return []operation{
		{
			method: http.MethodGet, path: "/api/v1/events", id: "streamEvents", tag: "events",
			summary:    "Stream CDC events with durable replay",
			pathParams: []any{eventsQuery{}},
			resps: []respUnit{
				{http.StatusOK, ""},
				{status: http.StatusBadRequest, body: envelope.APIError{}},
				{status: http.StatusInternalServerError, body: envelope.APIError{}},
				{status: http.StatusNotImplemented, body: envelope.APIError{}},
			},
			contentTypes: map[int]string{http.StatusOK: "text/event-stream"},
		},
		{
			method: http.MethodGet, path: "/api/v1/streams", id: "streamEventsOverWebSocket", tag: "events",
			summary: "Carry the daemon's event streams over one WebSocket (the desktop's alternative to one connection per SSE stream)",
			resps: []respUnit{
				{http.StatusSwitchingProtocols, ""},
				{status: http.StatusForbidden, body: envelope.APIError{}},
			},
		},
	}
}
