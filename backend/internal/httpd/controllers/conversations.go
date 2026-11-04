package controllers

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/attachmentstore"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
)

// Native chat images are sent to the provider and retained in conversation
// history. Workspace file attachments use separate, larger spawn limits. The
// body limit also fits a 25 MiB render page as a JSON string, but only because
// the page's HTML source is capped at 1 MiB: json.Marshal writes each <, > and
// & as a 6-byte escape, so the source can grow sixfold. Inlined images are
// base64 and do not grow.
const (
	maxConversationImageBytes  = 10 << 20
	maxConversationImagesBytes = 25 << 20
	maxConversationBody        = maxConversationImagesBytes*4/3 + (2 << 20)
)

// ConversationService is the controller-facing Chat contract.
type ConversationService interface {
	Snapshot(ctx context.Context, session domain.SessionID) (chatsvc.Snapshot, error)
	Send(ctx context.Context, session domain.SessionID, msg ports.ChatUserMessage) (domain.ConversationTurn, error)
	EditMessage(ctx context.Context, session domain.SessionID, turnID string, msg ports.ChatUserMessage) (chatsvc.EditMessageResult, error)
	ActivateBranch(ctx context.Context, session domain.SessionID, branchID string) (string, error)
	Resolve(ctx context.Context, session domain.SessionID, requestID string, decision ports.ChatDecision) error
	ResolveInput(ctx context.Context, session domain.SessionID, requestID string, response ports.ChatInputResponse) error
	Interrupt(ctx context.Context, session domain.SessionID) error
	Steer(ctx context.Context, session domain.SessionID, msg ports.ChatUserMessage) (chatsvc.SteerResult, error)
	RecoverSteer(ctx context.Context, session domain.SessionID, clientMessageID string) (chatsvc.SteerResult, error)
	PromoteQueuedTurn(ctx context.Context, session domain.SessionID, turnID string) (chatsvc.PromoteQueuedTurnResult, error)
	CancelQueuedTurn(ctx context.Context, session domain.SessionID, turnID string) error
	EditQueuedTurn(ctx context.Context, session domain.SessionID, turnID string, edit chatsvc.QueuedMessageEdit) error
	ReorderQueuedTurns(ctx context.Context, session domain.SessionID, turnIDs []string) error
	Models(ctx context.Context, session domain.SessionID) ([]ports.ChatModel, domain.ConversationSettings, error)
	ConfigOptions(ctx context.Context, session domain.SessionID) ([]ports.ChatConfigOption, error)
	SetConfigOption(ctx context.Context, session domain.SessionID, configID string, value ports.ChatConfigOptionValue) ([]ports.ChatConfigOption, error)
	Skills(ctx context.Context, session domain.SessionID) ([]ports.ChatSkill, error)
	SetTurnSettings(ctx context.Context, session domain.SessionID, settings domain.ConversationSettings) (domain.ConversationSettings, error)
	Compact(ctx context.Context, session domain.SessionID) (ports.ChatCompactionResult, error)
	Rollback(ctx context.Context, session domain.SessionID, turnID string) (int, error)
	RetryTurn(ctx context.Context, session domain.SessionID, turnID string) (domain.ConversationTurn, error)
	SetTitle(ctx context.Context, session domain.SessionID, title string) (string, error)
	ReloadMCPServers(ctx context.Context, session domain.SessionID) ([]domain.ConversationMCPServer, error)
	SwitchProvider(ctx context.Context, session domain.SessionID, request domain.ProviderSwitchRequest) error
	SwitchProviderAndSend(ctx context.Context, session domain.SessionID, request domain.ProviderSwitchRequest, msg ports.ChatUserMessage) (domain.ConversationTurn, bool, error)
	SessionHistory(ctx context.Context, session domain.SessionID, offset, limit int) ([]domain.HandoffTurn, int, error)
}

type pagedConversationService interface {
	SnapshotPage(ctx context.Context, session domain.SessionID, beforeSequence, limit int64) (chatsvc.Snapshot, error)
}

type reviewerConversationService interface {
	ModelsForOwner(context.Context, domain.ConversationOwner) ([]ports.ChatModel, domain.ConversationSettings, error)
	SetTurnSettingsForOwner(context.Context, domain.ConversationOwner, domain.ConversationSettings) (domain.ConversationSettings, error)
	SnapshotPageForReview(ctx context.Context, reviewID string, beforeSequence, limit int64) (chatsvc.Snapshot, error)
	SendForOwner(ctx context.Context, owner domain.ConversationOwner, msg ports.ChatUserMessage) (domain.ConversationTurn, error)
	ResolveForOwner(ctx context.Context, owner domain.ConversationOwner, requestID string, decision ports.ChatDecision) error
	ResolveInputForOwner(ctx context.Context, owner domain.ConversationOwner, requestID string, response ports.ChatInputResponse) error
	InterruptForOwner(ctx context.Context, owner domain.ConversationOwner) error
}

type chatViewService interface {
	SetChatView(context.Context, domain.SessionID, string, bool) error
}

type turnAutoResumeService interface {
	SetTurnAutoResume(context.Context, domain.SessionID, string, bool) error
}

// ConversationsController owns the Chat routes for a session.
//
// Every route dispatches from the session's persisted mode inside the service, so
// a client cannot reach the Chat path for a session that was created in TUI mode
// even by calling these URLs directly. UI visibility is not the boundary.
type ConversationsController struct {
	Svc ConversationService
	// SessionKind resolves a session's kind so orchestrator-authored steers to
	// another orchestrator are framed as untrusted; nil skips that framing.
	SessionKind func(context.Context, domain.SessionID) (domain.SessionKind, error)
	// Renders serves agent HTML renders. Nil answers the render route 501.
	Renders *attachmentstore.Store
	// MessageSent observes each message a session delivered; nil ignores them.
	MessageSent MessageSentFunc
	// Notifier tells the human when an orchestrator answers a worker prompt;
	// nil skips the notification.
	Notifier ApprovalNotifier
	// BackgroundTasks lists a session's running background tasks, so a stop
	// request can name only one that exists; nil answers the stop route 501.
	BackgroundTasks BackgroundTaskLister
	// Terminal answers permission dialogs of terminal-mode sessions, which
	// have no chat conversation; nil leaves them to the 409 mode mismatch.
	Terminal TerminalApprovalService
	// TerminalUsage reports spend of terminal-mode sessions; nil leaves their
	// usage answer to the chat-mode 409.
	TerminalUsage TerminalUsageService
}

// TerminalApprovalService reads and answers the permission dialog on a
// terminal-mode session's screen. supported is false for a session it cannot
// read, which keeps the chat-mode error.
type TerminalApprovalService interface {
	PendingTerminalApprovals(ctx context.Context, id domain.SessionID) (approvals []ports.PendingTerminalApproval, supported bool, err error)
	ResolveTerminalApproval(ctx context.Context, id domain.SessionID, requestID, decisionID string) error
}

// TerminalUsageService reads a terminal-mode session's spend from its agent's
// transcript. supported is false when the session has no such source.
type TerminalUsageService interface {
	TerminalModelUsage(ctx context.Context, id domain.SessionID) (usage []domain.ModelUsage, supported bool, err error)
}

func (c *ConversationsController) snapshot(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/conversation")
		return
	}
	session := domain.SessionID(chi.URLParam(r, "sessionId"))
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
	var snapshot chatsvc.Snapshot
	if paged, ok := c.Svc.(pagedConversationService); ok {
		snapshot, err = paged.SnapshotPage(r.Context(), session, before, limit)
	} else {
		snapshot, err = c.Svc.Snapshot(r.Context(), session)
	}
	if err != nil {
		writeConversationError(w, r, err)
		return
	}
	response := conversationSnapshotResponse(snapshot)
	// Excerpt verification is owned by Tallpine, independently of provider capabilities.
	// Older daemons ignore this request field, so clients must negotiate support.
	response.SupportsExcerpts = true
	envelope.WriteJSON(w, http.StatusOK, response)
}

func optionalPositiveInt64(raw string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 1 {
		return 0, fmt.Errorf("beforeSequence must be a positive integer")
	}
	return value, nil
}
