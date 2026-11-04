package apitypes

import (
	"tallpine.eu/tallpine/backend/internal/domain"
)

// ConversationImageContentRequest is a native raster image prompt block.
type ConversationImageContentRequest struct {
	MIMEType string `json:"mimeType"`
	Data     string `json:"data"`
}

// SessionHistoryResponse is one page of a session's turns, newest first.
type SessionHistoryResponse struct {
	Turns  []domain.HandoffTurn `json:"turns"`
	Total  int                  `json:"total"`
	Offset int                  `json:"offset"`
}

// SteerConversationRequest is guidance for a turn that is already running.
type SteerConversationRequest struct {
	// Text is the correction to hand the agent mid-turn.
	Text string `json:"text"`
	// SenderSessionID identifies the Tallpine session that authored an automation steer.
	// It is optional so older callers and in-app human steering remain unchanged.
	SenderSessionID string `json:"senderSessionId,omitempty"`
	// Attachments are native image prompt blocks delivered with the correction.
	Attachments []ConversationImageContentRequest `json:"attachments,omitempty"`
	// ClientMessageID makes a retry idempotent at Tallpine's durable daemon boundary. The
	// provider does not promise to honor this handle, so Tallpine reserves it before I/O
	// and replays only a known result on every later request.
	ClientMessageID string `json:"clientMessageId,omitempty"`
	// RecoverOnly reads the saved result for ClientMessageID without contacting the
	// provider. Native image bytes need not be resent after a renderer restart.
	RecoverOnly bool `json:"recoverOnly,omitempty"`
}

// SteerOrSendConversationResponse reports the single durable outcome selected by
// the atomic steer-or-send operation.
type SteerOrSendConversationResponse struct {
	Outcome        string           `json:"outcome" enum:"steered,sent,cleared"`
	TurnID         string           `json:"turnId,omitempty"`
	ProviderTurnID string           `json:"providerTurnId,omitempty"`
	ActivityID     string           `json:"activityId,omitempty"`
	State          domain.TurnState `json:"state,omitempty" enum:"queued,running,completed,recovered,interrupted,failed"`
	Duplicate      bool             `json:"duplicate"`
}
