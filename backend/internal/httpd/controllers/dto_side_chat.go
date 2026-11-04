package controllers

import (
	"tallpine.eu/tallpine/backend/internal/domain"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
)

// CreateConversationSideChatRequest opens an independent launch-scoped side chat.
type CreateConversationSideChatRequest struct {
	ForceNew       bool                                 `json:"forceNew,omitempty"`
	Label          string                               `json:"label,omitempty" maxLength:"80"`
	IdempotencyKey string                               `json:"idempotencyKey"`
	Reference      *ConversationExcerptReferenceRequest `json:"reference,omitempty"`
}

// CreateConversationSideChatResponse identifies a side without activating it.
type CreateConversationSideChatResponse struct {
	Side domain.SideConversation `json:"side"`
}

// SendSideQuestionRequest carries a side question and its references.
type SendSideQuestionRequest struct {
	Text            string                                `json:"text"`
	ClientMessageID string                                `json:"clientMessageId"`
	References      []ConversationExcerptReferenceRequest `json:"references,omitempty"`
	Attachments     []ConversationImageContentRequest     `json:"attachments,omitempty"`
	Resources       []ConversationResourceContentRequest  `json:"resources,omitempty"`
}

// SideQuestionResponse returns an accepted side question.
type SideQuestionResponse struct {
	Turn domain.SideTurn `json:"turn"`
}

// SideChatSnapshotResponse returns a side conversation snapshot.
type SideChatSnapshotResponse struct {
	Snapshot domain.SideSnapshot `json:"snapshot"`
}

// SideChatListResponse lists side conversations for a main chat.
type SideChatListResponse struct {
	Sides []domain.SideConversation `json:"sides"`
}

// SideSettingsRequest updates model settings for a side chat.
type SideSettingsRequest struct {
	Model  string `json:"model"`
	Effort string `json:"effort"`
}

// SideDraftRequest saves a launch-scoped side draft.
type SideDraftRequest struct {
	ContentJSON string `json:"contentJson"`
}

// SideDraftResponse returns a launch-scoped side draft.
type SideDraftResponse struct {
	ContentJSON string `json:"contentJson"`
}

// SideChatIDParam identifies a side chat in a route.
type SideChatIDParam struct {
	SideID string `path:"sideId" description:"Independent side chat identifier."`
}

// SideChatPageQuery controls side transcript pagination.
type SideChatPageQuery struct {
	Before string `query:"before,omitempty" description:"Read side turns older than this RFC3339 timestamp."`
	Limit  *int   `query:"limit,omitempty" minimum:"1" maximum:"500"`
}

// SideChatLaunchClaimRequest claims a desktop launch for side chat recovery.
type SideChatLaunchClaimRequest struct {
	AppRunID string `json:"appRunId"`
}

// SideChatLaunchState carries the side state retained by Electron for recovery.
type SideChatLaunchState struct {
	Numbers  map[domain.SessionID]int     `json:"numbers,omitempty"`
	AppRunID string                       `json:"appRunId"`
	Sides    []chatsvc.SideRecoveryRecord `json:"sides"`
}

// SideLabelRequest renames a side conversation.
type SideLabelRequest struct {
	Label string `json:"label"`
}
