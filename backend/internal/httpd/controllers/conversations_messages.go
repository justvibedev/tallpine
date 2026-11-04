package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
)

func (c *ConversationsController) editMessage(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST",
			"/api/v1/sessions/{sessionId}/conversation/turns/{turnId}/edit")
		return
	}
	var req EditConversationMessageRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"CHAT_EDIT_TURN_INVALID", "edited message text is required", nil)
		return
	}
	result, err := c.Svc.EditMessage(r.Context(), domain.SessionID(chi.URLParam(r, "sessionId")),
		chi.URLParam(r, "turnId"), ports.ChatUserMessage{
			Text: req.Text, ClientMessageID: req.ClientMessageID, Origin: domain.MessageOriginHuman,
		})
	if err != nil {
		writeConversationEditError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusAccepted, EditConversationMessageResponse{
		SourceBranchID: result.SourceBranchID,
		ActiveBranchID: result.ActiveBranchID,
		TurnID:         result.Turn.ID,
		ProviderTurnID: result.Turn.ProviderTurnID,
		State:          result.Turn.State,
	})
}

// retryTurn re-dispatches a failed turn's durable prompt as a new turn. The
// content is read from Tallpine's rows rather than the request, so the daemon owns
// what gets sent again and the original failed turn stays failed.
func (c *ConversationsController) retryTurn(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST",
			"/api/v1/sessions/{sessionId}/conversation/turns/{turnId}/retry")
		return
	}
	turn, err := c.Svc.RetryTurn(r.Context(),
		domain.SessionID(chi.URLParam(r, "sessionId")), chi.URLParam(r, "turnId"))
	if err != nil {
		writeConversationRetryError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusAccepted, RetryTurnResponse{
		TurnID:         turn.ID,
		ProviderTurnID: turn.ProviderTurnID,
		State:          turn.State,
	})
}

func (c *ConversationsController) setTurnAutoResume(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.Svc.(turnAutoResumeService)
	if !ok {
		apispec.NotImplemented(w, r, http.MethodPatch, "/api/v1/sessions/{sessionId}/conversation/turns/{turnId}/auto-resume")
		return
	}
	var req SetTurnAutoResumeRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); err != nil || req.Enabled == nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "CHAT_AUTO_RESUME_INVALID", "enabled must be a boolean", nil)
		return
	}
	if err := svc.SetTurnAutoResume(r.Context(), domain.SessionID(chi.URLParam(r, "sessionId")), chi.URLParam(r, "turnId"), *req.Enabled); err != nil {
		if errors.Is(err, chatsvc.ErrUsageLimitAutoResumeInvalid) || errors.Is(err, chatsvc.ErrUsageLimitResetUnknown) || errors.Is(err, ports.ErrAgentAccountChanged) {
			envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "CHAT_AUTO_RESUME_UNAVAILABLE", err.Error(), nil)
		} else {
			writeConversationRetryError(w, r, err)
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *ConversationsController) activateBranch(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST",
			"/api/v1/sessions/{sessionId}/conversation/branches/{branchId}/activate")
		return
	}
	branchID, err := url.PathUnescape(chi.URLParam(r, "branchId"))
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"CHAT_BRANCH_INVALID", "conversation branch identifier is invalid", nil)
		return
	}
	active, err := c.Svc.ActivateBranch(r.Context(), domain.SessionID(chi.URLParam(r, "sessionId")),
		branchID)
	if errors.Is(err, domain.ErrNoConversationBranch) {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found",
			"CHAT_BRANCH_NOT_FOUND", "that conversation branch does not exist", nil)
		return
	}
	if errors.Is(err, chatsvc.ErrTurnRunning) || errors.Is(err, chatsvc.ErrControllerHandoff) {
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_EDIT_BUSY", "stop the current turn before switching conversation branches", nil)
		return
	}
	if errors.Is(err, chatsvc.ErrBranchProviderMismatch) {
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_BRANCH_PROVIDER_MISMATCH",
			"that branch belongs to the agent that handled this session before the last switch", nil)
		return
	}
	if err != nil {
		writeConversationError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusAccepted, ActivateConversationBranchResponse{ActiveBranchID: active})
}

// rollback discards a turn and everything after it, from the agent's memory as well
// as from the timeline.
func (c *ConversationsController) rollback(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST",
			"/api/v1/sessions/{sessionId}/conversation/turns/{turnId}/rollback")
		return
	}
	discarded, err := c.Svc.Rollback(r.Context(),
		domain.SessionID(chi.URLParam(r, "sessionId")), chi.URLParam(r, "turnId"))
	if err != nil {
		writeConversationError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, RollbackConversationResponse{TurnsDiscarded: discarded})
}

// compact asks the provider to summarize earlier history and reclaim context.
//
// 202 rather than 200: the provider accepts the request and does the work as its
// own turn afterwards, so this reports acceptance and the reclaim lands on the
// timeline. Claiming 200 would say the compaction is done.
func (c *ConversationsController) compact(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/conversation/compact")
		return
	}
	result, err := c.Svc.Compact(r.Context(), domain.SessionID(chi.URLParam(r, "sessionId")))
	if err != nil {
		writeConversationError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusAccepted, CompactConversationResponse{
		TokensBefore: result.TokensBefore,
		TokensAfter:  result.TokensAfter,
	})
}

func (c *ConversationsController) send(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/conversation/messages")
		return
	}
	var req SendConversationMessageRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	message, invalid := chatUserMessage(req)
	if invalid != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", invalid.code, invalid.message, nil)
		return
	}
	turn, err := c.Svc.Send(r.Context(), domain.SessionID(chi.URLParam(r, "sessionId")), message)
	if err != nil {
		writeConversationError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusAccepted, sendConversationMessageResponse(turn))
}

// chatUserMessage validates a send request and turns it into the message the
// agent receives.
func chatUserMessage(req SendConversationMessageRequest) (ports.ChatUserMessage, *attachmentError) {
	if req.Text == "" && len(req.Attachments) == 0 && len(req.Resources) == 0 && len(req.Excerpts) == 0 {
		// There is no keystroke concept in Chat mode: an empty body is a client
		// bug, not a way to nudge the agent.
		return ports.ChatUserMessage{}, &attachmentError{code: "CHAT_MESSAGE_EMPTY", message: "message text is required"}
	}
	content, attachmentErr := conversationContent(req)
	if attachmentErr != nil {
		return ports.ChatUserMessage{}, attachmentErr
	}
	text := req.Text
	if text == "" {
		if len(req.Excerpts) > 0 {
			text = fmt.Sprintf("Use the attached %d chat excerpt(s) as context", len(req.Excerpts))
		} else {
			text = fmt.Sprintf("Attached %d item(s) for context", len(content))
		}
	}
	excerpts := make([]ports.ChatExcerptReference, 0, len(req.Excerpts))
	for _, excerpt := range req.Excerpts {
		excerpts = append(excerpts, ports.ChatExcerptReference{
			ConversationID: excerpt.ConversationID, MessageID: excerpt.MessageID,
			Revision: excerpt.Revision, Text: excerpt.Text,
		})
	}
	return ports.ChatUserMessage{
		Text:            text,
		Content:         content,
		Excerpts:        excerpts,
		ClientMessageID: req.ClientMessageID,
		Origin:          domain.MessageOriginHuman,
		Continuation:    req.Continuation,
	}, nil
}

// sendConversationMessageResponse reports an accepted send. An empty turn means
// the client message id was already delivered. Reporting the duplicate
// explicitly lets a retrying client stop rather than assume a new turn began.
func sendConversationMessageResponse(turn domain.ConversationTurn) SendConversationMessageResponse {
	return SendConversationMessageResponse{
		TurnID:         turn.ID,
		ProviderTurnID: turn.ProviderTurnID,
		State:          turn.State,
		Duplicate:      turn.ID == "" && turn.ContextClearedActivityID == "",
		Cleared:        turn.ContextClearedActivityID != "",
		ActivityID:     turn.ContextClearedActivityID,
	}
}

func conversationContent(req SendConversationMessageRequest) ([]ports.ChatContent, *attachmentError) {
	spawnInputs := make([]AttachmentInput, 0, len(req.Attachments))
	for _, attachment := range req.Attachments {
		spawnInputs = append(spawnInputs, AttachmentInput{
			MimeType: attachment.MIMEType,
			Data:     attachment.Data,
		})
	}
	if _, err := decodeAttachments(spawnInputs, maxConversationImageBytes, maxConversationImagesBytes); err != nil {
		return nil, err
	}
	content := make([]ports.ChatContent, 0, len(req.Attachments)+len(req.Resources))
	for _, attachment := range req.Attachments {
		content = append(content, ports.ChatContent{
			Type: "image", Data: attachment.Data,
			MIMEType: strings.ToLower(strings.TrimSpace(attachment.MIMEType)),
		})
	}
	for _, resource := range req.Resources {
		if strings.TrimSpace(resource.URI) == "" || strings.TrimSpace(resource.Name) == "" {
			return nil, &attachmentError{"INVALID_RESOURCE", "resource uri and name are required"}
		}
		if resource.URI == ports.ChatInternalReplayResourceURI {
			return nil, &attachmentError{"INVALID_RESOURCE", "resource uri is reserved for Tallpine internal context"}
		}
		kind, text := "resource_link", ""
		if resource.Text != nil {
			kind = "resource"
			text = *resource.Text
		}
		content = append(content, ports.ChatContent{
			Type: kind, URI: resource.URI, Name: resource.Name,
			MIMEType: resource.MIMEType, Text: text,
		})
	}
	return content, nil
}

// agentRestarter is the optional Chat service ability to restart a quiet
// agent in place, keeping the conversation.
type agentRestarter interface {
	RestartChatAgent(ctx context.Context, session domain.SessionID) error
}

// restartAgent restarts the agent so a saved launch-time setting, such as the
// auto-compaction window, takes effect. The conversation history is kept.
func (c *ConversationsController) restartAgent(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/conversation/restart-agent")
		return
	}
	restarter, ok := c.Svc.(agentRestarter)
	if !ok {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/conversation/restart-agent")
		return
	}
	err := restarter.RestartChatAgent(r.Context(), domain.SessionID(chi.URLParam(r, "sessionId")))
	if errors.Is(err, chatsvc.ErrRestartBusy) {
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "CHAT_AGENT_BUSY", err.Error(), nil)
		return
	}
	if err != nil {
		writeConversationError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *ConversationsController) interrupt(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/conversation/interrupt")
		return
	}
	err := c.Svc.Interrupt(r.Context(), domain.SessionID(chi.URLParam(r, "sessionId")))
	if err != nil {
		writeConversationError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
