package controllers

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
)

func (c *ConversationsController) resolve(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST",
			"/api/v1/sessions/{sessionId}/conversation/approvals/{requestId}/resolve")
		return
	}
	var req ResolveConversationApprovalRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	if req.DecisionID == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"CHAT_DECISION_REQUIRED", "decisionId is required", nil)
		return
	}

	requestID, ok := conversationRequestID(w, r)
	if !ok {
		return
	}
	session := domain.SessionID(chi.URLParam(r, "sessionId"))
	answer, allowed := c.agentResolveActor(w, r, session, requestID, req.DecisionID)
	if !allowed {
		return
	}
	note := approvalNote(answer.actor, req.Note)
	err := c.Svc.Resolve(r.Context(), session, requestID,
		ports.ChatDecision{ID: req.DecisionID, ResolvedBy: answer.actor, Note: note})
	if errors.Is(err, chatsvc.ErrNotChatMode) && c.Terminal != nil {
		err = c.Terminal.ResolveTerminalApproval(r.Context(), session, requestID, req.DecisionID)
		if errors.Is(err, ports.ErrTerminalApprovalNotPending) {
			envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found",
				"APPROVAL_NOT_PENDING", "no pending approval has that request id", nil)
			return
		}
	}
	if err != nil {
		writeConversationError(w, r, err)
		return
	}
	auditAnswer(session, requestID, note, answer)
	c.notifyAnswer(r, session, requestID, note, answer)
	w.WriteHeader(http.StatusNoContent)
}

func (c *ConversationsController) resolveInput(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST",
			"/api/v1/sessions/{sessionId}/conversation/inputs/{requestId}/resolve")
		return
	}
	var req ResolveConversationInputRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	action := ports.ChatInputAction(req.Action)
	if !action.Valid() {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"CHAT_INPUT_ACTION_INVALID", "action must be accept, decline, or cancel", nil)
		return
	}
	if action != ports.ChatInputActionAccept && len(req.Content) > 0 {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"CHAT_INPUT_CONTENT_INVALID", "content is only allowed with accept", nil)
		return
	}
	requestID, ok := conversationRequestID(w, r)
	if !ok {
		return
	}
	session := domain.SessionID(chi.URLParam(r, "sessionId"))
	agent, isAgent := requestscope.AgentFrom(r.Context())
	var answer orchestratorAnswer
	if isAgent {
		pending, ok := c.pendingInput(w, r, session, requestID)
		if !ok {
			return
		}
		answer = orchestratorAnswer{
			actor: "orchestrator:" + string(agent.SessionID), project: agent.ProjectID,
			decision: string(action), kind: "input_" + string(action), verb: inputVerb(action),
			summary: truncateRunes(pending.Summary, maxAuditText),
		}
	}
	if err := c.Svc.ResolveInput(
		r.Context(), session, requestID,
		ports.ChatInputResponse{Action: action, Content: req.Content},
	); err != nil {
		writeConversationError(w, r, err)
		return
	}
	auditAnswer(session, requestID, "", answer)
	c.notifyAnswer(r, session, requestID, "", answer)
	w.WriteHeader(http.StatusNoContent)
}

// inputVerb is how the human is told an orchestrator answered a question.
func inputVerb(action ports.ChatInputAction) string {
	switch action {
	case ports.ChatInputActionAccept:
		return "accepted a question"
	case ports.ChatInputActionDecline:
		return "declined a question"
	}
	return "cancelled a question"
}

// pendingInput finds the open question an agent is answering, refusing any id
// that is not a listed pending request so an orchestrator cannot probe others.
func (c *ConversationsController) pendingInput(w http.ResponseWriter, r *http.Request, session domain.SessionID, requestID string) (PendingInput, bool) {
	inputs, err := c.pendingInputs(r, session)
	if err != nil {
		writeConversationError(w, r, err)
		return PendingInput{}, false
	}
	for _, in := range inputs {
		if in.RequestID == requestID {
			return in, true
		}
	}
	envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found",
		"INPUT_NOT_PENDING", "no pending question has that request id", nil)
	return PendingInput{}, false
}
