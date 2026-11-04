package specgen

import (
	"net/http"

	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
)

func shellTerminalOperationsPart3() []operation {
	return []operation{
		{
			method: http.MethodGet, path: "/api/v1/reviews/{reviewId}/conversation", id: "getReviewerConversation", tag: "conversations",
			summary: "Read a reviewer's durable Chat conversation", pathParams: []any{controllers.ReviewIDParam{}, conversationSnapshotQuery{}},
			resps: []respUnit{{http.StatusOK, controllers.ConversationSnapshotResponse{}}, {http.StatusBadRequest, envelope.APIError{}}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusConflict, envelope.APIError{}}, {http.StatusInternalServerError, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodPost, path: "/api/v1/reviews/{reviewId}/conversation/messages", id: "sendReviewerConversationMessage", tag: "conversations",
			summary: "Send a message to a Chat reviewer", pathParams: []any{controllers.ReviewIDParam{}}, reqBody: controllers.SendConversationMessageRequest{},
			resps: []respUnit{{http.StatusAccepted, controllers.SendConversationMessageResponse{}}, {http.StatusBadRequest, envelope.APIError{}}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusConflict, envelope.APIError{}}, {http.StatusInternalServerError, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodPost, path: "/api/v1/reviews/{reviewId}/conversation/approvals/{requestId}/resolve", id: "resolveReviewerConversationApproval", tag: "conversations",
			summary: "Answer a pending approval in a reviewer conversation", pathParams: []any{controllers.ReviewIDParam{}, controllers.ConversationRequestIDParam{}}, reqBody: controllers.ResolveConversationApprovalRequest{},
			resps: []respUnit{{http.StatusNoContent, nil}, {http.StatusBadRequest, envelope.APIError{}}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusConflict, envelope.APIError{}}, {http.StatusInternalServerError, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodPost, path: "/api/v1/reviews/{reviewId}/conversation/inputs/{requestId}/resolve", id: "resolveReviewerConversationInput", tag: "conversations",
			summary: "Answer a structured reviewer input request", pathParams: []any{controllers.ReviewIDParam{}, controllers.ConversationRequestIDParam{}}, reqBody: controllers.ResolveConversationInputRequest{},
			resps: []respUnit{{http.StatusNoContent, nil}, {http.StatusBadRequest, envelope.APIError{}}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusConflict, envelope.APIError{}}, {http.StatusInternalServerError, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodPost, path: "/api/v1/reviews/{reviewId}/conversation/interrupt", id: "interruptReviewerConversationTurn", tag: "conversations",
			summary: "Cancel the in-flight reviewer turn", pathParams: []any{controllers.ReviewIDParam{}},
			resps: []respUnit{{http.StatusNoContent, nil}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusConflict, envelope.APIError{}}, {http.StatusInternalServerError, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/conversation/steer", id: "steerSessionConversationTurn", tag: "conversations",
			summary:    "Send guidance into the in-flight turn of a chat session",
			pathParams: []any{controllers.SessionIDParam{}},
			reqBody:    controllers.SteerConversationRequest{},
			resps: []respUnit{
				{http.StatusAccepted, controllers.SteerConversationResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/renders", id: "publishSessionRender", tag: "conversations",
			summary:    "Show an agent's self-contained HTML page inline in its chat thread",
			pathParams: []any{controllers.SessionIDParam{}},
			reqBody:    controllers.PublishRenderRequest{},
			resps: []respUnit{
				{http.StatusCreated, controllers.PublishRenderResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/renders/check", id: "checkSessionRender", tag: "conversations",
			summary:    "Screenshot an agent's HTML page in the desktop app before it is published",
			pathParams: []any{controllers.SessionIDParam{}},
			reqBody:    controllers.RenderCheckRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.RenderCheckResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusUnprocessableEntity, envelope.APIError{}},
				{http.StatusServiceUnavailable, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/sessions/{sessionId}/renders/{renderId}", id: "getSessionRender", tag: "conversations",
			summary:    "Serve a published render as a sandboxed HTML document",
			pathParams: []any{controllers.SessionIDParam{}, controllers.RenderIDParam{}, renderSourceQuery{}},
			resps: []respUnit{
				{http.StatusOK, ""},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
			contentTypes: map[int]string{http.StatusOK: "text/html"},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/renders/{renderId}/artifact", id: "saveSessionRenderArtifact", tag: "conversations",
			summary:    "Keep a published render as a session artifact",
			pathParams: []any{controllers.SessionIDParam{}, controllers.RenderIDParam{}},
			reqBody:    controllers.SaveRenderArtifactRequest{},
			resps: []respUnit{
				{http.StatusCreated, controllers.SaveRenderArtifactResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/conversation/steer-or-send", id: "steerOrSendSessionConversationTurn", tag: "conversations",
			summary:    "Steer the active turn or send a new turn when idle",
			pathParams: []any{controllers.SessionIDParam{}},
			reqBody:    controllers.SteerConversationRequest{},
			resps: []respUnit{
				{http.StatusAccepted, controllers.SteerOrSendConversationResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/background-tasks/stop", id: "stopSessionBackgroundTask", tag: "conversations",
			summary:    "Stop one of the session agent's background tasks",
			pathParams: []any{controllers.SessionIDParam{}},
			reqBody:    controllers.StopBackgroundTaskRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.StopBackgroundTaskResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/conversation/turns/{turnId}/steer", id: "promoteQueuedSessionConversationTurn", tag: "conversations",
			summary:    "Promote a queued message into the in-flight turn",
			pathParams: []any{controllers.SessionIDParam{}, controllers.ConversationTurnIDParam{}},
			resps: []respUnit{
				{http.StatusAccepted, controllers.PromoteQueuedTurnResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/conversation/turns/{turnId}/cancel", id: "cancelQueuedSessionConversationTurn", tag: "conversations",
			summary:    "Remove one queued message without stopping the running turn",
			pathParams: []any{controllers.SessionIDParam{}, controllers.ConversationTurnIDParam{}},
			resps: []respUnit{
				{http.StatusNoContent, nil},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/conversation/turns/{turnId}/queue/edit", id: "editQueuedSessionConversationTurn", tag: "conversations",
			summary:    "Rewrite one queued message before it dispatches",
			pathParams: []any{controllers.SessionIDParam{}, controllers.ConversationTurnIDParam{}},
			reqBody:    controllers.EditQueuedConversationMessageRequest{},
			resps: []respUnit{
				{http.StatusNoContent, nil},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/conversation/queue/reorder", id: "reorderQueuedSessionConversationTurns", tag: "conversations",
			summary:    "Rewrite the durable queue order for undispatched turns",
			pathParams: []any{controllers.SessionIDParam{}},
			reqBody:    controllers.ReorderQueuedConversationTurnsRequest{},
			resps: []respUnit{
				{http.StatusNoContent, nil},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/conversation/turns/{turnId}/rollback", id: "rollbackSessionConversation", tag: "conversations",
			summary:    "Discard a turn and everything after it from the agent's memory",
			pathParams: []any{controllers.SessionIDParam{}, controllers.ConversationTurnIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.RollbackConversationResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPatch, path: "/api/v1/sessions/{sessionId}/conversation/turns/{turnId}/auto-resume", id: "setSessionConversationTurnAutoResume", tag: "conversations",
			summary: "Schedule or cancel a usage-limited turn retry", pathParams: []any{controllers.SessionIDParam{}, controllers.ConversationTurnIDParam{}}, reqBody: controllers.SetTurnAutoResumeRequest{},
			resps: []respUnit{{http.StatusNoContent, nil}, {http.StatusBadRequest, envelope.APIError{}}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusConflict, envelope.APIError{}}, {http.StatusInternalServerError, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/conversation/turns/{turnId}/retry", id: "retrySessionConversationTurn", tag: "conversations",
			summary:    "Re-dispatch a failed turn's durable prompt as a new turn",
			pathParams: []any{controllers.SessionIDParam{}, controllers.ConversationTurnIDParam{}},
			resps: []respUnit{
				{http.StatusAccepted, controllers.RetryTurnResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPut, path: "/api/v1/sessions/{sessionId}/conversation/title", id: "setSessionConversationTitle", tag: "conversations",
			summary:    "Name the provider's conversation thread",
			pathParams: []any{controllers.SessionIDParam{}},
			reqBody:    controllers.SetConversationTitleRequest{},
			resps: []respUnit{
				{http.StatusAccepted, controllers.SetConversationTitleResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/shell-terminals", id: "listShellTerminals", tag: "shellTerminals",
			summary: "List the standalone shell terminals owned by the current app run",
			resps: []respUnit{
				{http.StatusOK, controllers.ListShellTerminalsResponse{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/shell-terminals", id: "openShellTerminal", tag: "shellTerminals",
			summary: "Open a standalone shell terminal",
			reqBody: controllers.OpenShellTerminalRequest{},
			resps: []respUnit{
				{http.StatusCreated, controllers.ShellTerminalEnvelope{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPatch, path: "/api/v1/shell-terminals/{handleId}", id: "renameShellTerminal", tag: "shellTerminals",
			summary:    "Rename a standalone shell terminal tab",
			pathParams: []any{controllers.ShellTerminalHandleIDParam{}},
			reqBody:    controllers.UpdateShellTerminalRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.ShellTerminalEnvelope{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodDelete, path: "/api/v1/shell-terminals/{handleId}", id: "closeShellTerminal", tag: "shellTerminals",
			summary:    "Close a standalone shell terminal and destroy its PTY",
			pathParams: []any{controllers.ShellTerminalHandleIDParam{}},
			resps: []respUnit{
				{http.StatusNoContent, nil},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
	}
}
