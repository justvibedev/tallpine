package controllers

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
)

func (c *ConversationsController) reviewService(w http.ResponseWriter, r *http.Request) (reviewerConversationService, bool) {
	svc, ok := c.Svc.(reviewerConversationService)
	if !ok {
		apispec.NotImplemented(w, r, r.Method, r.URL.Path)
	}
	return svc, ok
}

func (c *ConversationsController) reviewModels(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.reviewService(w, r)
	if !ok {
		return
	}
	models, selected, err := svc.ModelsForOwner(r.Context(), domain.ReviewConversationOwner(chi.URLParam(r, "reviewId")))
	if err != nil && !errors.Is(err, chatsvc.ErrModelsUnsupported) {
		writeConversationError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, conversationModelsResponse(models, selected))
}

func (c *ConversationsController) reviewSetSettings(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.reviewService(w, r)
	if !ok {
		return
	}
	var req ConversationTurnSettingsPayload
	if !decodeConversationBody(w, r, &req) {
		return
	}
	approval := domain.PermissionMode(req.ApprovalMode)
	if req.ApprovalMode != "" && !approval.Valid() {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "CHAT_APPROVAL_MODE_INVALID", "unknown approval mode", nil)
		return
	}
	settings, err := svc.SetTurnSettingsForOwner(r.Context(), domain.ReviewConversationOwner(chi.URLParam(r, "reviewId")), domain.ConversationSettings{Model: req.Model, ReasoningEffort: req.ReasoningEffort, ApprovalMode: approval})
	if errors.Is(err, chatsvc.ErrReviewerPermissionsFixed) {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "CHAT_REVIEW_PERMISSIONS_FIXED", err.Error(), nil)
		return
	}
	if writeModelPolicyError(w, r, err) {
		return
	}
	if err != nil {
		writeConversationError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, turnSettingsPayload(settings))
}

func (c *ConversationsController) reviewSnapshot(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.reviewService(w, r)
	if !ok {
		return
	}
	before, err := optionalPositiveInt64(r.URL.Query().Get("beforeSequence"))
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "CONVERSATION_CURSOR_INVALID", err.Error(), nil)
		return
	}
	limit := int64(200)
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || limit < 1 || limit > 500 {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "CONVERSATION_LIMIT_INVALID", "limit must be between 1 and 500", nil)
			return
		}
	}
	snapshot, err := svc.SnapshotPageForReview(r.Context(), chi.URLParam(r, "reviewId"), before, limit)
	if err != nil {
		writeConversationError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, conversationSnapshotResponse(snapshot))
}

func (c *ConversationsController) reviewSend(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.reviewService(w, r)
	if !ok {
		return
	}
	var req SendConversationMessageRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	if req.Text == "" && len(req.Attachments) == 0 && len(req.Resources) == 0 {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "CHAT_MESSAGE_EMPTY", "message text is required", nil)
		return
	}
	content, attachmentErr := conversationContent(req)
	if attachmentErr != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", attachmentErr.code, attachmentErr.message, nil)
		return
	}
	text := req.Text
	if text == "" {
		text = fmt.Sprintf("Attached %d item(s) for context", len(content))
	}
	turn, err := svc.SendForOwner(r.Context(), domain.ReviewConversationOwner(chi.URLParam(r, "reviewId")), ports.ChatUserMessage{Text: text, Content: content, ClientMessageID: req.ClientMessageID, Origin: domain.MessageOriginHuman})
	if err != nil {
		writeConversationError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusAccepted, SendConversationMessageResponse{TurnID: turn.ID, ProviderTurnID: turn.ProviderTurnID, State: turn.State, Duplicate: turn.ID == ""})
}

func (c *ConversationsController) reviewResolve(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.reviewService(w, r)
	if !ok {
		return
	}
	var req ResolveConversationApprovalRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	if req.DecisionID == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "CHAT_DECISION_REQUIRED", "decisionId is required", nil)
		return
	}
	requestID, ok := conversationRequestID(w, r)
	if !ok {
		return
	}
	if err := svc.ResolveForOwner(r.Context(), domain.ReviewConversationOwner(chi.URLParam(r, "reviewId")), requestID, ports.ChatDecision{ID: req.DecisionID}); err != nil {
		writeConversationError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *ConversationsController) reviewResolveInput(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.reviewService(w, r)
	if !ok {
		return
	}
	var req ResolveConversationInputRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	action := ports.ChatInputAction(req.Action)
	if !action.Valid() {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "CHAT_INPUT_ACTION_INVALID", "action must be accept, decline, or cancel", nil)
		return
	}
	if action != ports.ChatInputActionAccept && len(req.Content) > 0 {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "CHAT_INPUT_CONTENT_INVALID", "content is only allowed with accept", nil)
		return
	}
	requestID, ok := conversationRequestID(w, r)
	if !ok {
		return
	}
	if err := svc.ResolveInputForOwner(r.Context(), domain.ReviewConversationOwner(chi.URLParam(r, "reviewId")), requestID, ports.ChatInputResponse{Action: action, Content: req.Content}); err != nil {
		writeConversationError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *ConversationsController) reviewInterrupt(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.reviewService(w, r)
	if !ok {
		return
	}
	if err := svc.InterruptForOwner(r.Context(), domain.ReviewConversationOwner(chi.URLParam(r, "reviewId"))); err != nil {
		writeConversationError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
