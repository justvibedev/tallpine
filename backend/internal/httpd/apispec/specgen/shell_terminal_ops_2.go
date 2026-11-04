package specgen

import (
	"net/http"

	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
)

func shellTerminalOperationsPart2() []operation {
	return []operation{
		{
			method: http.MethodGet, path: "/api/v1/sessions/{sessionId}/history", id: "getSessionHistory", tag: "conversations",
			summary:    "Read a session's turns, newest first, across Chat and Terminal",
			pathParams: []any{controllers.SessionIDParam{}, controllers.SessionHistoryQuery{}},
			resps: []respUnit{
				{http.StatusOK, controllers.SessionHistoryResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/conversation/turns/{turnId}/edit", id: "editSessionConversationMessage", tag: "conversations",
			summary:    "Branch before and replace an earlier human prompt",
			pathParams: []any{controllers.SessionIDParam{}, controllers.ConversationTurnIDParam{}},
			reqBody:    controllers.EditConversationMessageRequest{},
			resps: []respUnit{
				{http.StatusAccepted, controllers.EditConversationMessageResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/conversation/branches/{branchId}/activate", id: "activateSessionConversationBranch", tag: "conversations",
			summary:    "Resume a durable conversation branch without sending",
			pathParams: []any{controllers.SessionIDParam{}, controllers.ConversationBranchIDParam{}},
			resps: []respUnit{
				{http.StatusAccepted, controllers.ActivateConversationBranchResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/conversation/side-chats", id: "createSessionConversationSideChat", tag: "conversations",
			summary:    "Open an independent launch-scoped side chat",
			pathParams: []any{controllers.SessionIDParam{}},
			reqBody:    controllers.CreateConversationSideChatRequest{},
			resps: []respUnit{
				{http.StatusCreated, controllers.CreateConversationSideChatResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/sessions/{sessionId}/conversation/side-chats", id: "listSessionSideChats", tag: "conversations",
			pathParams: []any{controllers.SessionIDParam{}},
			resps:      []respUnit{{http.StatusOK, controllers.SideChatListResponse{}}, {http.StatusConflict, envelope.APIError{}}},
		},
		{
			method: http.MethodGet, path: "/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}", id: "getSessionSideChat", tag: "conversations",
			pathParams: []any{controllers.SessionIDParam{}, controllers.SideChatIDParam{}, controllers.SideChatPageQuery{}},
			resps:      []respUnit{{http.StatusOK, controllers.SideChatSnapshotResponse{}}, {http.StatusBadRequest, envelope.APIError{}}, {http.StatusNotFound, envelope.APIError{}}},
		},
		{
			method: http.MethodDelete, path: "/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}", id: "deleteSessionSideChat", tag: "conversations",
			pathParams: []any{controllers.SessionIDParam{}, controllers.SideChatIDParam{}},
			resps:      []respUnit{{http.StatusNoContent, nil}, {http.StatusNotFound, envelope.APIError{}}},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/messages", id: "sendSessionSideChatQuestion", tag: "conversations",
			pathParams: []any{controllers.SessionIDParam{}, controllers.SideChatIDParam{}}, reqBody: controllers.SendSideQuestionRequest{},
			resps: []respUnit{{http.StatusAccepted, controllers.SideQuestionResponse{}}, {http.StatusConflict, envelope.APIError{}}},
		},
		{
			method: http.MethodPatch, path: "/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/turns/{turnId}", id: "editSessionQueuedSideQuestion", tag: "conversations",
			pathParams: []any{controllers.SessionIDParam{}, controllers.SideChatIDParam{}, controllers.ConversationTurnIDParam{}}, reqBody: controllers.SendSideQuestionRequest{},
			resps: []respUnit{{http.StatusNoContent, nil}, {http.StatusConflict, envelope.APIError{}}},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/turns/{turnId}/retry", id: "retrySessionSideQuestion", tag: "conversations",
			pathParams: []any{controllers.SessionIDParam{}, controllers.SideChatIDParam{}, controllers.ConversationTurnIDParam{}},
			resps:      []respUnit{{http.StatusAccepted, nil}, {http.StatusConflict, envelope.APIError{}}},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/interrupt", id: "interruptSessionSideQuestion", tag: "conversations",
			pathParams: []any{controllers.SessionIDParam{}, controllers.SideChatIDParam{}},
			resps:      []respUnit{{http.StatusNoContent, nil}, {http.StatusConflict, envelope.APIError{}}},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/recover", id: "recoverSessionSideChat", tag: "conversations",
			pathParams: []any{controllers.SessionIDParam{}, controllers.SideChatIDParam{}},
			resps:      []respUnit{{http.StatusAccepted, nil}, {http.StatusConflict, envelope.APIError{}}, {http.StatusNotFound, envelope.APIError{}}},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/compact", id: "compactSessionSideChat", tag: "conversations",
			pathParams: []any{controllers.SessionIDParam{}, controllers.SideChatIDParam{}},
			resps: []respUnit{{http.StatusAccepted, controllers.CompactConversationResponse{}},
				{http.StatusConflict, envelope.APIError{}}, {http.StatusNotFound, envelope.APIError{}}},
		},
		{
			method: http.MethodGet, path: "/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/events", id: "streamSessionSideChat", tag: "conversations",
			pathParams:   []any{controllers.SessionIDParam{}, controllers.SideChatIDParam{}},
			resps:        []respUnit{{http.StatusOK, nil}, {http.StatusNotFound, envelope.APIError{}}},
			contentTypes: map[int]string{http.StatusOK: "text/event-stream"},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/approvals/{requestId}/resolve", id: "resolveSessionSideChatApproval", tag: "conversations",
			pathParams: []any{controllers.SessionIDParam{}, controllers.SideChatIDParam{}, controllers.ConversationRequestIDParam{}},
			reqBody:    controllers.ResolveConversationApprovalRequest{},
			resps:      []respUnit{{http.StatusNoContent, nil}, {http.StatusConflict, envelope.APIError{}}},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/inputs/{requestId}/resolve", id: "resolveSessionSideChatInput", tag: "conversations",
			pathParams: []any{controllers.SessionIDParam{}, controllers.SideChatIDParam{}, controllers.ConversationRequestIDParam{}},
			reqBody:    controllers.ResolveConversationInputRequest{},
			resps:      []respUnit{{http.StatusNoContent, nil}, {http.StatusConflict, envelope.APIError{}}},
		},
		{
			method: http.MethodPatch, path: "/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/settings", id: "updateSessionSideChatSettings", tag: "conversations",
			pathParams: []any{controllers.SessionIDParam{}, controllers.SideChatIDParam{}}, reqBody: controllers.SideSettingsRequest{},
			resps: []respUnit{{http.StatusNoContent, nil}, {http.StatusConflict, envelope.APIError{}}},
		},
		{
			method: http.MethodGet, path: "/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/draft", id: "getSessionSideChatDraft", tag: "conversations",
			pathParams: []any{controllers.SessionIDParam{}, controllers.SideChatIDParam{}},
			resps:      []respUnit{{http.StatusOK, controllers.SideDraftResponse{}}, {http.StatusNotFound, envelope.APIError{}}},
		},
		{
			method: http.MethodPut, path: "/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/draft", id: "putSessionSideChatDraft", tag: "conversations",
			pathParams: []any{controllers.SessionIDParam{}, controllers.SideChatIDParam{}}, reqBody: controllers.SideDraftRequest{},
			resps: []respUnit{{http.StatusNoContent, nil}, {http.StatusNotFound, envelope.APIError{}}},
		},
		{
			method: http.MethodPost, path: "/api/v1/side-chats/launch/claim", id: "claimSideChatLaunch", tag: "conversations",
			reqBody: controllers.SideChatLaunchClaimRequest{},
			resps:   []respUnit{{http.StatusNoContent, nil}, {http.StatusConflict, envelope.APIError{}}},
		},
		{
			method: http.MethodGet, path: "/api/v1/side-chats/launch/state", id: "exportSideChatLaunch", tag: "conversations",
			resps: []respUnit{{http.StatusOK, controllers.SideChatLaunchState{}}, {http.StatusConflict, envelope.APIError{}}},
		},
		{
			method: http.MethodPost, path: "/api/v1/side-chats/launch/state", id: "recoverSideChatLaunch", tag: "conversations",
			reqBody: controllers.SideChatLaunchState{},
			resps:   []respUnit{{http.StatusNoContent, nil}, {http.StatusConflict, envelope.APIError{}}},
		},
		{
			method: http.MethodPost, path: "/api/v1/side-chats/launch/retire", id: "retireSideChatLaunch", tag: "conversations",
			reqBody: controllers.SideChatLaunchClaimRequest{},
			resps:   []respUnit{{http.StatusNoContent, nil}, {http.StatusConflict, envelope.APIError{}}},
		},
		{
			method: http.MethodGet, path: "/api/v1/sessions/{sessionId}/conversation/approvals", id: "listSessionConversationApprovals", tag: "conversations",
			summary:    "List the pending approvals of a chat session, with the exact requests",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.PendingApprovalsResponse{}},
				{http.StatusForbidden, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/conversation/approvals/{requestId}/resolve", id: "resolveSessionConversationApproval", tag: "conversations",
			summary:    "Answer a pending approval in a chat session",
			pathParams: []any{controllers.SessionIDParam{}, controllers.ConversationRequestIDParam{}},
			reqBody:    controllers.ResolveConversationApprovalRequest{},
			resps: []respUnit{
				{http.StatusNoContent, nil},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusForbidden, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/conversation/inputs/{requestId}/resolve", id: "resolveSessionConversationInput", tag: "conversations",
			summary:    "Answer a structured input request in a chat session",
			pathParams: []any{controllers.SessionIDParam{}, controllers.ConversationRequestIDParam{}},
			reqBody:    controllers.ResolveConversationInputRequest{},
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
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/conversation/compact", id: "compactSessionConversation", tag: "conversations",
			summary:    "Summarize earlier history to reclaim context in a chat session",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusAccepted, controllers.CompactConversationResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/conversation/mcp/reload", id: "reloadSessionConversationMcpServers", tag: "conversations",
			summary:    "Restart the tool servers a chat session can reach",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.ReloadConversationMCPServersResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/sessions/{sessionId}/conversation/models", id: "listSessionConversationModels", tag: "conversations",
			summary:    "List the models the provider offers for a chat session",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.ConversationModelsResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/sessions/{sessionId}/conversation/config-options", id: "listSessionConversationConfigOptions", tag: "conversations",
			summary:    "List the live session controls the provider advertises",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.ConversationConfigOptionsResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPatch, path: "/api/v1/sessions/{sessionId}/conversation/config-options/{configId}", id: "setSessionConversationConfigOption", tag: "conversations",
			summary:    "Choose one provider-advertised session configuration value",
			pathParams: []any{controllers.SessionIDParam{}, controllers.ConversationConfigIDParam{}},
			reqBody:    controllers.SetConversationConfigOptionRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.ConversationConfigOptionsResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/sessions/{sessionId}/conversation/skills", id: "listSessionConversationSkills", tag: "conversations",
			summary:    "List the named skills the provider offers for a chat session",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.ConversationSkillsResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPatch, path: "/api/v1/sessions/{sessionId}/conversation/settings", id: "setSessionConversationTurnSettings", tag: "conversations",
			summary:    "Choose the model, reasoning effort and approval mode for the next turn",
			pathParams: []any{controllers.SessionIDParam{}},
			reqBody:    controllers.ConversationTurnSettingsPayload{},
			resps: []respUnit{
				{http.StatusOK, controllers.ConversationTurnSettingsPayload{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/conversation/interrupt", id: "interruptSessionConversationTurn", tag: "conversations",
			summary:    "Cancel the in-flight turn in a chat session",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusNoContent, nil},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/sessions/{sessionId}/usage", id: "getSessionModelUsage", tag: "conversations",
			summary:    "Cumulative token usage of a session by model (input, cache creation, cache read, output)",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.ModelUsageResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/sessions/{sessionId}/conversation/context-breakdown", id: "getSessionContextBreakdown", tag: "conversations",
			summary:    "What fills a running chat agent's context window, as the agent reports it (on request; available=false when it cannot)",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.ContextBreakdownResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/conversation/restart-agent", id: "restartSessionConversationAgent", tag: "conversations",
			summary:    "Restart a chat session's agent in place, keeping the conversation, so launch-time settings such as the auto-compaction window apply",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusNoContent, nil},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/reviews/{reviewId}/conversation/models", id: "listReviewerConversationModels", tag: "conversations",
			summary: "List the models offered for a Chat reviewer", pathParams: []any{controllers.ReviewIDParam{}},
			resps: []respUnit{{http.StatusOK, controllers.ConversationModelsResponse{}}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusConflict, envelope.APIError{}}, {http.StatusInternalServerError, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodPatch, path: "/api/v1/reviews/{reviewId}/conversation/settings", id: "setReviewerConversationSettings", tag: "conversations",
			summary: "Select model and effort for the reviewer's next Chat turn", pathParams: []any{controllers.ReviewIDParam{}}, reqBody: controllers.ConversationTurnSettingsPayload{},
			resps: []respUnit{{http.StatusOK, controllers.ConversationTurnSettingsPayload{}}, {http.StatusBadRequest, envelope.APIError{}}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusConflict, envelope.APIError{}}, {http.StatusInternalServerError, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
	}
}
