package controllers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/bodylimit"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
	sessionsvc "tallpine.eu/tallpine/backend/internal/service/session"
)

func (c *SessionsController) send(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/send")
		return
	}
	bodylimit.Raise(w, r, maxSendBodyBytes)
	var in SendSessionMessageRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if in.Message == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "MESSAGE_REQUIRED", "Message is required", nil)
		return
	}
	if len(in.Message) > maxMessageLen {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "MESSAGE_TOO_LONG", "Message is too long", nil)
		return
	}
	var attachment *ports.SpawnAttachment
	if in.Attachment != nil {
		decoded, attachErr := decodeAttachment(*in.Attachment, maxAttachmentBytes)
		if attachErr != nil {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", attachErr.code, attachErr.message, nil)
			return
		}
		attachment = &decoded
	}
	if agent, ok := scopedAgent(r); ok {
		// An agent speaks as itself: it can neither pass as the user nor as
		// another session.
		in.SenderSessionID = string(agent.SessionID)
		in.UserAuthored = false
	}
	message := domain.SanitizeControlChars(in.Message)
	if agent, ok := scopedAgent(r); ok {
		message = domain.NeutralizeOwnerStamp(message)
		if agent.Role != requestscope.RoleOrchestrator {
			message = domain.FrameUntrusted(string(agent.SessionID), message)
		} else if target, err := c.Svc.Get(r.Context(), sessionID(r)); err != nil || target.Kind == domain.KindOrchestrator {
			// Another lane's orchestrator may be prompt-injected too; an
			// unknown target is framed (fail closed).
			message = domain.FrameUntrustedOrchestrator(string(agent.SessionID), message)
		}
	}
	if in.RelayOwnerMessage != "" {
		block, code, msg := c.ownerRelayBlock(r, in.RelayOwnerMessage)
		if code != "" {
			envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", code, msg, nil)
			return
		}
		message = block + "\n\n" + message
	}
	var err error
	var receipt ports.MessageReceipt
	if receiptSender, ok := c.Svc.(sessionMessageReceiptSender); ok {
		receipt, err = receiptSender.SendWithReceipt(r.Context(), sessionID(r), message, attachment, ports.MessageDeliveryOptions{AuthoredByUser: in.UserAuthored, SenderSessionID: in.SenderSessionID})
	} else if in.UserAuthored || in.SenderSessionID != "" {
		sender, ok := c.Svc.(sessionMessageOptionsSender)
		if !ok {
			apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/send")
			return
		}
		err = sender.SendWithOptions(r.Context(), sessionID(r), message, attachment, ports.MessageDeliveryOptions{AuthoredByUser: in.UserAuthored, SenderSessionID: in.SenderSessionID})
	} else {
		err = c.Svc.Send(r.Context(), sessionID(r), message, attachment)
	}
	if errors.Is(err, chatsvc.ErrSteerDeliveryUncertain) {
		// The message was steered into the running turn and the provider's
		// reply was lost: it may already be with the agent, so a resend could
		// deliver it twice.
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "SEND_DELIVERY_UNCERTAIN",
			"the message may already have reached the agent; do not resend it", nil)
		return
	}
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	if c.MessageSent != nil {
		c.MessageSent(r.Context(), domain.SessionID(in.SenderSessionID), sessionID(r))
	}
	envelope.WriteJSON(w, http.StatusOK, SendSessionMessageResponse{OK: true, SessionID: sessionID(r), Message: message, Outcome: string(receipt.Outcome), DeliveryID: receipt.ID, WaitingOnInput: receipt.WaitingOnInput})
}

func (c *SessionsController) delegateTask(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/orchestrators/delegate")
		return
	}
	bodylimit.Raise(w, r, maxSpawnBodyBytes)
	var in DelegateTaskRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if !validClientRequestID(in.ClientRequestID) {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "INVALID_CLIENT_REQUEST_ID", "clientRequestId must be 1-128 visible characters", nil)
		return
	}
	if in.ProjectID == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "PROJECT_ID_REQUIRED", "projectId is required", nil)
		return
	}
	if len(in.Brief) > maxPromptLen {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "TASK_TOO_LONG", "Task must be 16 KiB or fewer", nil)
		return
	}
	if utf8.RuneCountInString(strings.TrimSpace(in.Model)) > maxModelLen {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "MODEL_TOO_LONG", "Model must be 256 characters or fewer", nil)
		return
	}
	if in.Mode != "" {
		mode, err := domain.ParseSessionMode(string(in.Mode))
		if err != nil {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_SESSION_MODE", "mode must be chat or tui", nil)
			return
		}
		in.Mode = mode
	}
	if !in.ApprovalMode.Valid() {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_APPROVAL_MODE", "approvalMode is invalid", nil)
		return
	}
	attachments, attachErr := decodeSpawnAttachments(in.Attachments)
	if attachErr != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", attachErr.code, attachErr.message, nil)
		return
	}
	requestHash := ""
	if in.ClientRequestID != "" {
		keyless := in
		keyless.ClientRequestID = ""
		keyless.TaskPreparation = ""
		keyless.Attachments = nil
		requestHash = clientRequestFingerprint("delegate", keyless, attachments)
	}

	backlogItem, warnings, ok := c.prepareBacklogSpawn(w, r, "POST", "/api/v1/orchestrators/delegate", in.ProjectID, in.BacklogRef, in.BacklogForce, in.BacklogAllowDuplicate, "separate")
	if !ok {
		return
	}

	out, err := c.Svc.DelegateTask(r.Context(), sessionsvc.DelegateTaskInput{
		BacklogContext:    backlogItem.Context,
		ClientRequestID:   in.ClientRequestID,
		ClientRequestHash: requestHash,
		ProjectID:         in.ProjectID,
		Brief:             domain.SanitizeControlChars(in.Brief),
		RequestedAgent:    in.Agent,
		Model:             domain.SanitizeControlChars(strings.TrimSpace(in.Model)),
		Effort:            sanitizedOptionalString(in.Effort),
		ApprovalMode:      in.ApprovalMode,
		RequestedMode:     in.Mode,
		Attachments:       attachments,
		TaskPreparation:   domain.TaskPreparationToken(strings.TrimSpace(in.TaskPreparation)),
	})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	warnings = c.linkBacklogSession(r, backlogItem, out.WorkerID, "", warnings)
	envelope.WriteJSON(w, http.StatusAccepted, DelegateTaskResponse{OK: true, WorkerID: out.WorkerID, OrchestratorID: out.OrchestratorID, Warnings: warnings})
}

func validClientRequestID(id string) bool {
	return id == "" || len(id) <= maxIdempotencyKey && strings.TrimSpace(id) == id && strings.IndexFunc(id, unicode.IsControl) < 0
}

func clientRequestFingerprint(route string, request any, attachments []ports.SpawnAttachment) string {
	h := sha256.New()
	_ = json.NewEncoder(h).Encode(struct {
		Route   string
		Request any
	}{route, request})
	for _, attachment := range attachments {
		_, _ = fmt.Fprintf(h, "%d:%s:%d:", len(attachment.Ext), attachment.Ext, len(attachment.Data))
		_, _ = h.Write(attachment.Data)
	}
	return "v1:" + hex.EncodeToString(h.Sum(nil))
}

func (c *SessionsController) prepareTask(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/projects/{id}/tasks/prepare")
		return
	}
	token, err := c.Svc.PrepareTask(r.Context(), projectID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusAccepted, PrepareTaskResponse{OK: true, TaskPreparation: token})
}

func (c *SessionsController) cancelTaskPreparation(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "DELETE", "/api/v1/task-preparations/{token}")
		return
	}
	if err := c.Svc.CancelTaskPreparation(r.Context(), chi.URLParam(r, "token")); err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func sanitizedOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	sanitized := domain.SanitizeControlChars(strings.TrimSpace(*value))
	return &sanitized
}

// scopeAgentSpawn holds an orchestrator's spawn to its own project: a worker
// whose parent is the orchestrator itself, with the permission policy the
// daemon derives from that parent rather than one the request chooses. It
// fills defaults in place and returns why the request is refused, if it is.
func scopeAgentSpawn(ctx context.Context, agent requestscope.Agent, in *SpawnSessionRequest) string {
	eval := false
	switch {
	case in.ProjectID == "":
		in.ProjectID = agent.ProjectID
	case !agent.MayActOn(in.ProjectID):
		// An eval project the orchestrator's project created (B335).
		if !agent.EvalAccess(ctx, in.ProjectID) {
			return "spawn a session in another project"
		}
		eval = true
	}
	if in.Kind != domain.KindWorker {
		return "spawn an orchestrator"
	}
	if in.ApprovalMode != "" {
		return "choose a spawned session's approval mode; it inherits the orchestrator's"
	}
	switch in.ParentSessionID {
	case "":
		if !eval { // a session of another project is not this orchestrator's child
			in.ParentSessionID = agent.SessionID
		}
	case agent.SessionID:
	default:
		return "spawn on behalf of another session"
	}
	return ""
}

// agentClaimTakeoverGuard keeps a scoped agent from claiming a PR away from a
// session outside its scope: a worker may claim only a PR nobody else owns,
// and an orchestrator only from sessions of its own project. Terminated owners
// are checked too, and the check runs inside the claim transaction.
func agentClaimTakeoverGuard(agent requestscope.Agent) func(ports.ClaimOwner) error {
	return func(owner ports.ClaimOwner) error {
		if owner.SessionID == agent.SessionID {
			return nil
		}
		if agent.Role == requestscope.RoleOrchestrator && agent.ProjectID != "" && agent.MayActOn(owner.ProjectID) {
			return nil
		}
		return apierr.Forbidden("AGENT_SCOPE_FORBIDDEN",
			"An agent session token cannot take a pull request from session "+string(owner.SessionID)+"; ask its project's orchestrator or the user")
	}
}
