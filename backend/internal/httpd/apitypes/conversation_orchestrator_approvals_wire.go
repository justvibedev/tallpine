package apitypes

import (
	"encoding/json"
	"time"
)

// PendingApproval is an unanswered permission prompt of a session, with the
// provider's exact request (command, tool input) in Detail so the reader can
// decide without opening the session.
type PendingApproval struct {
	RequestID string                    `json:"requestId"`
	Summary   string                    `json:"summary"`
	Detail    json.RawMessage           `json:"detail,omitempty"`
	Decisions []PendingApprovalDecision `json:"decisions"`
	CreatedAt time.Time                 `json:"createdAt"`
}

// PendingApprovalDecision is one answer a pending approval offers.
type PendingApprovalDecision struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Kind  string `json:"kind,omitempty"`
}

// PendingInput is an unanswered in-chat question (a structured form or URL
// request) of a session. It is answered with the input route, not a decision.
type PendingInput struct {
	RequestID string          `json:"requestId"`
	Summary   string          `json:"summary"`
	Detail    json.RawMessage `json:"detail,omitempty"`
	CreatedAt time.Time       `json:"createdAt"`
}

// PendingApprovalsResponse lists the pending approvals and in-chat questions of
// one session.
type PendingApprovalsResponse struct {
	SessionID string            `json:"sessionId"`
	Approvals []PendingApproval `json:"approvals"`
	Inputs    []PendingInput    `json:"inputs"`
}
