package controllers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/bodylimit"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
)

// writeConversationRetryError maps retry refusals to their HTTP meanings.
func writeConversationRetryError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, chatsvc.ErrTurnNotRetryable):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_RETRY_NOT_RETRYABLE",
			"this turn cannot be retried: it is not a failed human turn in this conversation", nil)
	case errors.Is(err, chatsvc.ErrRetryDeliveryUncertain):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_RETRY_DELIVERY_UNCERTAIN",
			"delivery of this turn was never confirmed by the agent, so retrying it could run the work twice", nil)
	case errors.Is(err, chatsvc.ErrRetryStaleBranch):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_RETRY_STALE_BRANCH",
			"this turn is no longer on the active conversation branch", nil)
	case errors.Is(err, chatsvc.ErrRetryContentInvalid):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"CHAT_RETRY_CONTENT_INVALID", err.Error(), nil)
	case errors.Is(err, chatsvc.ErrRetryUnsupported):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_RETRY_UNSUPPORTED", err.Error(), nil)
	case errors.Is(err, chatsvc.ErrTurnRunning), errors.Is(err, chatsvc.ErrControllerHandoff):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_RETRY_BUSY", "stop the current turn before retrying this one", nil)
	case errors.Is(err, domain.ErrNoConversationTurn):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found",
			"CHAT_RETRY_TURN_NOT_FOUND", "that turn is not in this conversation", nil)
	default:
		writeConversationError(w, r, err)
	}
}

func writeConversationEditError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, chatsvc.ErrEditDeliveryUncertain):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_EDIT_UNCERTAIN",
			"the provider may have received this edit; retry only with the same recovery action", nil)
	case errors.Is(err, chatsvc.ErrEditIdempotencyConflict):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_EDIT_IDEMPOTENCY_CONFLICT",
			"this delivery handle belongs to a different edit", nil)
	case errors.Is(err, chatsvc.ErrEditDeliveryRejected):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_EDIT_REJECTED",
			"the edit was not accepted; retry deliberately with a new delivery handle", nil)
	case errors.Is(err, chatsvc.ErrForkUnsupported):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_EDIT_UNSUPPORTED", "this agent cannot branch an earlier prompt", nil)
	case errors.Is(err, chatsvc.ErrTurnRunning), errors.Is(err, chatsvc.ErrControllerHandoff):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_EDIT_BUSY", "stop the current turn before branching", nil)
	case errors.Is(err, chatsvc.ErrEditTurnInvalid) && errors.Is(err, domain.ErrNoConversationTurn):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found",
			"CHAT_EDIT_TURN_INVALID", "that editable prompt is not in this conversation", nil)
	case errors.Is(err, chatsvc.ErrEditTurnInvalid):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"CHAT_EDIT_TURN_INVALID", err.Error(), nil)
	default:
		writeConversationError(w, r, err)
	}
}

// conversationRequestID decodes the path id the Electron client percent-encodes.
// ACP persistent hosts mint ids such as `acp-request:<host>:<n>`; openapi-fetch
// turns the colons into %3A, and chi.URLParam leaves that encoding in place —
// the same trap ActivateBranch already handles for `conversation-1:root`.
func conversationRequestID(w http.ResponseWriter, r *http.Request) (string, bool) {
	requestID, err := url.PathUnescape(chi.URLParam(r, "requestId"))
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"CHAT_REQUEST_INVALID", "conversation request identifier is invalid", nil)
		return "", false
	}
	return requestID, true
}

func decodeConversationBody(w http.ResponseWriter, r *http.Request, into any) bool {
	return decodeConversationBodyLimit(w, r, into, maxConversationBody)
}

func decodeConversationBodyLimit(w http.ResponseWriter, r *http.Request, into any, limit int64) bool {
	bodylimit.Raise(w, r, limit)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"INVALID_BODY", "could not read request body", nil)
		return false
	}
	if err := json.Unmarshal(body, into); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"INVALID_BODY", "request body is not valid JSON", nil)
		return false
	}
	return true
}

// writeConversationError maps the Chat service's typed failures onto stable
// codes, so a client can tell a permanent answer from a retryable one.
func writeConversationError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ports.ErrSessionNotFound):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found",
			"SESSION_NOT_FOUND", "session not found", nil)

	case errors.Is(err, chatsvc.ErrNotChatMode):
		// Permanent for this request: retry only after an explicit interface switch.
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"SESSION_MODE_MISMATCH",
			"this session was created in Terminal UI mode and has no chat conversation", nil)

	case errors.Is(err, chatsvc.ErrNoController), errors.Is(err, chatsvc.ErrNotProvisioning):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_CONTROLLER_NOT_READY",
			"the agent controller for this session is not running", nil)

	case errors.Is(err, chatsvc.ErrContinuationUnavailable):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_CONTINUATION_UNAVAILABLE", err.Error(), nil)

	case errors.Is(err, chatsvc.ErrExcerptInvalid):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"CHAT_EXCERPT_INVALID", err.Error(), nil)

	case errors.Is(err, chatsvc.ErrExcerptStale):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_EXCERPT_STALE", err.Error(), nil)

	case errors.Is(err, chatsvc.ErrPromptSuggestionStale):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_PROMPT_SUGGESTION_STALE", err.Error(), nil)

	case errors.Is(err, chatsvc.ErrControllerHandoff):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_INTERFACE_TRANSITION",
			"the session is switching interfaces; send again after the handoff completes", nil)

	case errors.Is(err, chatsvc.ErrClearUnsupported):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_CLEAR_UNSUPPORTED", err.Error(), nil)

	case errors.Is(err, chatsvc.ErrClearWhileBusy):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_CLEAR_WHILE_BUSY", err.Error(), nil)

	case errors.Is(err, chatsvc.ErrCompactionUnsupported):
		// Permanent for this harness, and a 409 rather than a 500 because nothing
		// failed: the provider simply has no way to reclaim context. A client should
		// stop offering the control instead of retrying.
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_COMPACTION_UNSUPPORTED",
			"this agent cannot compact its conversation history", nil)

	case errors.Is(err, chatsvc.ErrCompactionWhileBusy):
		// Retryable once the turn ends, and refused rather than forwarded: the
		// provider would silently interrupt the running turn to make room.
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_COMPACTION_BUSY",
			"finish or stop the current turn before compacting", nil)

	case errors.Is(err, chatsvc.ErrNoActiveTurn):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_NO_ACTIVE_TURN", "there is no turn in flight to interrupt", nil)

	case errors.Is(err, domain.ErrNoConversationTurn):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found",
			"CHAT_TURN_NOT_FOUND", "that turn is not in this session's conversation", nil)

	case errors.Is(err, domain.ErrClientMessageConflict):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_MESSAGE_IDEMPOTENCY_CONFLICT", "clientMessageId belongs to a different message", nil)

	case errors.Is(err, chatsvc.ErrTurnRunning):
		// Retryable, unlike every other refusal here: the same request works once
		// the agent finishes. Rolling back mid-turn is refused rather than raced,
		// because discarding history the agent is still writing into would leave
		// Tallpine's timeline and the agent's memory describing different conversations.
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_TURN_RUNNING",
			"stop the agent before rolling back: it is in the middle of a turn", nil)

	case errors.Is(err, chatsvc.ErrTurnNotRollbackable):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_TURN_NOT_ROLLBACKABLE",
			"that turn never reached the agent, so there is nothing to undo", nil)

	case errors.Is(err, chatsvc.ErrTurnProviderMismatch):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_TURN_PROVIDER_MISMATCH",
			"that turn belongs to the agent that handled this session before the last switch", nil)

	case errors.Is(err, chatsvc.ErrRollbackUnsupported):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_ROLLBACK_UNSUPPORTED", "this agent cannot discard conversation history", nil)

	case errors.Is(err, chatsvc.ErrForkUnsupported):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_FORK_UNSUPPORTED", "this agent cannot branch a conversation", nil)

	case errors.Is(err, chatsvc.ErrRenameUnsupported):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_RENAME_UNSUPPORTED", "this agent's conversation carries no title", nil)

	case errors.Is(err, chatsvc.ErrOrchestratorRename):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"ORCHESTRATOR_RENAME_UNSUPPORTED", "the project orchestrator cannot be renamed", nil)

	case errors.Is(err, chatsvc.ErrMCPReloadUnsupported):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_MCP_RELOAD_UNSUPPORTED", "this agent cannot reload its tool servers", nil)

	case errors.Is(err, chatsvc.ErrTitleRequired):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"CHAT_TITLE_REQUIRED", "title is required", nil)

	case errors.Is(err, chatsvc.ErrProviderRefused):
		// The provider declined and the conversation is fine. Its own explanation is
		// carried through: it says something the user can act on, which a generic
		// conflict message would throw away.
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_PROVIDER_REFUSED", err.Error(), nil)

	case errors.Is(err, ports.ErrChatRequestNotPending):
		// Two clients can watch the same approval, so arriving second is ordinary.
		// The card is stale; the client should refresh rather than retry.
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_REQUEST_NOT_PENDING",
			"this request has already been answered or is no longer waiting", nil)

	case errors.Is(err, ports.ErrChatDecisionNotOffered):
		// A client asking for an option the provider never offered is a bug in the
		// client, not a server failure — and the request is still pending, so the
		// user can still answer it.
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"CHAT_DECISION_NOT_OFFERED", err.Error(), nil)

	case errors.Is(err, ports.ErrChatConfigOptionInvalid):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"CHAT_CONFIG_OPTION_INVALID", err.Error(), nil)

	case errors.Is(err, ports.ErrChatUnsupported):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"SESSION_MODE_UNSUPPORTED", err.Error(), nil)

	case errors.Is(err, ports.ErrChatDriverUnavailable):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_DRIVER_UNAVAILABLE", err.Error(), nil)

	case errors.Is(err, ports.ErrChatDriverIncompatible):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_DRIVER_INCOMPATIBLE", err.Error(), nil)

	case errors.Is(err, ports.ErrChatAuthRequired):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_AUTH_REQUIRED", "the agent is installed but not authenticated", nil)

	case errors.Is(err, ports.ErrChatResumeFailed):
		// Deliberately not a silent recovery: the client must offer the user a
		// choice rather than have Tallpine invent a fresh conversation.
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_RESUME_FAILED",
			"the stored provider conversation could not be resumed", nil)

	default:
		envelope.WriteError(w, r, err)
	}
}

// writeModelPolicyError answers a model the project policy refuses (403) or a
// policy that could not be read (503), and reports whether it wrote a response.
func writeModelPolicyError(w http.ResponseWriter, r *http.Request, err error) bool {
	var denied *domain.ModelDeniedError
	switch {
	case errors.As(err, &denied):
		envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", "MODEL_DENIED", err.Error(), nil)
	case errors.Is(err, chatsvc.ErrModelPolicyUnavailable):
		envelope.WriteAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "MODEL_POLICY_UNAVAILABLE", err.Error(), nil)
	default:
		return false
	}
	return true
}
