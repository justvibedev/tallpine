package controllers

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
)

// Register mounts the conversation routes under a session.
func (c *ConversationsController) Register(r chi.Router) {
	r.Patch("/sessions/{sessionId}/conversation/side-chats/{sideId}/label", c.renameSideChat)
	r.Post("/sessions/{sessionId}/conversation/side-chats/{sideId}/turns/{turnId}/cancel", c.cancelSideQuestion)
	r.Post("/sessions/{sessionId}/conversation/side-chats", c.createSideChat)
	r.Get("/sessions/{sessionId}/conversation/side-chats", c.listSideChats)
	r.Get("/sessions/{sessionId}/conversation/side-chats/{sideId}", c.sideSnapshot)
	r.Get("/sessions/{sessionId}/conversation/side-chats/{sideId}/events", c.streamSideChat)
	r.Delete("/sessions/{sessionId}/conversation/side-chats/{sideId}", c.closeSideChat)
	r.Post("/sessions/{sessionId}/conversation/side-chats/{sideId}/messages", c.sendSideQuestion)
	r.Patch("/sessions/{sessionId}/conversation/side-chats/{sideId}/turns/{turnId}", c.editSideQuestion)
	r.Post("/sessions/{sessionId}/conversation/side-chats/{sideId}/turns/{turnId}/retry", c.retrySideQuestion)
	r.Post("/sessions/{sessionId}/conversation/side-chats/{sideId}/interrupt", c.interruptSideQuestion)
	r.Post("/sessions/{sessionId}/conversation/side-chats/{sideId}/recover", c.recoverSideChat)
	r.Post("/sessions/{sessionId}/conversation/side-chats/{sideId}/compact", c.compactSideChat)
	r.Post("/sessions/{sessionId}/conversation/side-chats/{sideId}/approvals/{requestId}/resolve", c.resolveSideApproval)
	r.Post("/sessions/{sessionId}/conversation/side-chats/{sideId}/inputs/{requestId}/resolve", c.resolveSideInput)
	r.Patch("/sessions/{sessionId}/conversation/side-chats/{sideId}/settings", c.sideSettings)
	r.Get("/sessions/{sessionId}/conversation/side-chats/{sideId}/draft", c.getSideDraft)
	r.Put("/sessions/{sessionId}/conversation/side-chats/{sideId}/draft", c.putSideDraft)
	r.Post("/side-chats/launch/claim", c.claimSideLaunch)
	r.Get("/side-chats/launch/state", c.exportSideLaunch)
	r.Post("/side-chats/launch/state", c.recoverSideLaunch)
	r.Post("/side-chats/launch/retire", c.retireSideLaunch)
	r.Post("/sessions/{sessionId}/chat-view", c.setChatView)
	r.Get("/sessions/{sessionId}/conversation", c.snapshot)
	r.Post("/sessions/{sessionId}/conversation/messages", c.send)
	r.Post("/sessions/{sessionId}/conversation/prompt-suggestion", c.promptSuggestion)
	r.Get("/sessions/{sessionId}/conversation/approvals", c.listApprovals)
	r.Post("/sessions/{sessionId}/conversation/approvals/{requestId}/resolve", c.resolve)
	r.Post("/sessions/{sessionId}/conversation/inputs/{requestId}/resolve", c.resolveInput)
	r.Post("/sessions/{sessionId}/conversation/interrupt", c.interrupt)
	r.Post("/sessions/{sessionId}/conversation/restart-agent", c.restartAgent)
	r.Get("/sessions/{sessionId}/conversation/context-breakdown", c.contextBreakdown)
	r.Post("/orchestrators/{id}/research", c.setOrchestratorResearch)
	r.Get("/orchestrators/{id}/research", c.getOrchestratorResearch)
	r.Get("/sessions/{sessionId}/usage", c.modelUsage)
	r.Post("/sessions/{sessionId}/conversation/steer", c.steer)
	r.Post("/sessions/{sessionId}/conversation/steer-or-send", c.steerOrSend)
	r.Post("/sessions/{sessionId}/background-tasks/stop", c.stopBackgroundTask)
	r.Post("/sessions/{sessionId}/conversation/turns/{turnId}/steer", c.promoteQueuedTurn)
	r.Post("/sessions/{sessionId}/conversation/turns/{turnId}/cancel", c.cancelQueuedTurn)
	r.Post("/sessions/{sessionId}/conversation/turns/{turnId}/queue/edit", c.editQueuedTurn)
	r.Post("/sessions/{sessionId}/conversation/queue/reorder", c.reorderQueuedTurns)
	r.Post("/sessions/{sessionId}/conversation/compact", c.compact)
	r.Post("/agents/{agent}/chat/prewarm", c.prewarmAgent)
	r.Get("/agents/chat/unavailable", c.unavailableChatAgents)
	r.Get("/agents/chat/permission-modes", c.chatPermissionModes)
	r.Get("/sessions/{sessionId}/conversation/models", c.models)
	r.Get("/sessions/{sessionId}/conversation/config-options", c.configOptions)
	r.Patch("/sessions/{sessionId}/conversation/config-options/{configId}", c.setConfigOption)
	r.Get("/sessions/{sessionId}/conversation/skills", c.skills)
	r.Patch("/sessions/{sessionId}/conversation/settings", c.setSettings)
	r.Post("/sessions/{sessionId}/conversation/turns/{turnId}/rollback", c.rollback)
	r.Post("/sessions/{sessionId}/conversation/turns/{turnId}/edit", c.editMessage)
	r.Post("/sessions/{sessionId}/conversation/turns/{turnId}/retry", c.retryTurn)
	r.Patch("/sessions/{sessionId}/conversation/turns/{turnId}/auto-resume", c.setTurnAutoResume)
	r.Post("/sessions/{sessionId}/conversation/branches/{branchId}/activate", c.activateBranch)
	r.Put("/sessions/{sessionId}/conversation/title", c.setTitle)
	r.Post("/sessions/{sessionId}/conversation/mcp/reload", c.reloadMCPServers)
	r.Post("/sessions/{sessionId}/provider-switch", c.switchProvider)
	r.Get("/sessions/{sessionId}/history", c.sessionHistory)
	r.Post("/sessions/{sessionId}/renders", c.publishRender)
	r.Post("/sessions/{sessionId}/renders/check", c.checkRender)
	r.Get("/sessions/{sessionId}/renders/{renderId}", c.renderFile)
	r.Post("/sessions/{sessionId}/renders/{renderId}/artifact", c.saveRenderArtifact)
	r.Get("/reviews/{reviewId}/conversation/models", c.reviewModels)
	r.Patch("/reviews/{reviewId}/conversation/settings", c.reviewSetSettings)
	r.Get("/reviews/{reviewId}/conversation", c.reviewSnapshot)
	r.Post("/reviews/{reviewId}/conversation/messages", c.reviewSend)
	r.Post("/reviews/{reviewId}/conversation/approvals/{requestId}/resolve", c.reviewResolve)
	r.Post("/reviews/{reviewId}/conversation/inputs/{requestId}/resolve", c.reviewResolveInput)
	r.Post("/reviews/{reviewId}/conversation/interrupt", c.reviewInterrupt)
}

func (c *ConversationsController) setChatView(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.Svc.(chatViewService)
	if !ok {
		apispec.NotImplemented(w, r, http.MethodPost, "/api/v1/sessions/{sessionId}/chat-view")
		return
	}
	var req SetChatViewRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	if req.ViewID == "" || len(req.ViewID) > 128 || strings.TrimSpace(req.ViewID) != req.ViewID {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "CHAT_VIEW_ID_INVALID", "viewId must be a nonempty identifier of at most 128 bytes", nil)
		return
	}
	if !req.activePresent {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "CHAT_VIEW_ACTIVE_INVALID", "active must be true or false", nil)
		return
	}
	if err := svc.SetChatView(r.Context(), sessionID(r), req.ViewID, req.Active); err != nil {
		writeConversationError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
