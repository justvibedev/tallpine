package controllers

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
)

type sideChatConversationService interface {
	SideChatNumbers(string, map[domain.SessionID]int) map[domain.SessionID]int
	RenameSideChat(context.Context, domain.SessionID, string, string) error
	CancelQueuedSideQuestion(context.Context, domain.SessionID, string, string) error
	ListIndependentSideChats(context.Context, domain.SessionID) ([]domain.SideConversation, error)
	CreateIndependentSideChat(context.Context, domain.SessionID, chatsvc.SideCreateRequest) (domain.SideConversation, error)
	SideSnapshot(context.Context, domain.SessionID, string, time.Time, int) (domain.SideSnapshot, error)
	SendSideQuestion(context.Context, domain.SessionID, string, ports.ChatUserMessage) (domain.SideTurn, error)
	EditQueuedSideQuestion(context.Context, domain.SessionID, string, string, string) error
	RetrySideQuestion(context.Context, domain.SessionID, string, string) error
	RetrySideConnection(context.Context, domain.SessionID, string) error
	UpdateSideSettings(context.Context, domain.SessionID, string, string, string) error
	SaveSideDraft(context.Context, domain.SessionID, string, string) error
	SideDraft(context.Context, domain.SessionID, string) (string, error)
	InterruptSideQuestion(context.Context, domain.SessionID, string) error
	CompactSideChat(context.Context, domain.SessionID, string) (ports.ChatCompactionResult, error)
	ResolveSideApproval(context.Context, domain.SessionID, string, string, string) error
	ResolveSideInput(context.Context, domain.SessionID, string, string, ports.ChatInputResponse) error
	CloseIndependentSideChat(context.Context, domain.SessionID, string) error
	ClaimSideChatLaunch(context.Context, string) error
	ExportSideChatLaunch(context.Context, string) ([]chatsvc.SideRecoveryRecord, error)
	RecoverSideChatLaunch(context.Context, string, []chatsvc.SideRecoveryRecord) error
	RetireSideChatLaunch(context.Context, string) error
	WatchSideChat(context.Context, domain.SessionID, string) (string, <-chan struct{}, func(), error)
}

func (c *ConversationsController) createSideChat(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/conversation/side-chats")
		return
	}
	var req CreateConversationSideChatRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	svc, ok := c.sideService(w, r)
	if !ok {
		return
	}

	var ref *ports.ChatExcerptReference
	if req.Reference != nil {
		ref = &ports.ChatExcerptReference{ConversationID: req.Reference.ConversationID,
			MessageID: req.Reference.MessageID, Revision: req.Reference.Revision, Text: req.Reference.Text}
	}
	side, err := svc.CreateIndependentSideChat(r.Context(), domain.SessionID(chi.URLParam(r, "sessionId")),
		chatsvc.SideCreateRequest{IdempotencyKey: req.IdempotencyKey, ForceNew: req.ForceNew, Label: req.Label, Reference: ref})
	if err != nil {
		writeSideChatError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, CreateConversationSideChatResponse{Side: side})
}
