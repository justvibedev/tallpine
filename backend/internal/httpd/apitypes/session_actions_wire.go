package apitypes

import (
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// BrowserCommandRequest is the stable daemon-facing command envelope. Action
// arguments remain action-specific JSON so new target-scoped operations do not
// require a new transport or Electron IPC surface.
type BrowserCommandRequest struct {
	SessionID domain.SessionID       `json:"sessionId"`
	Action    string                 `json:"action"`
	Args      map[string]interface{} `json:"args,omitempty"`
}

// BrowserCommandResponse returns a correlated result from the browser runtime.
type BrowserCommandResponse struct {
	RequestID string           `json:"requestId"`
	SessionID domain.SessionID `json:"sessionId"`
	Action    string           `json:"action"`
	Result    interface{}      `json:"result"`
}

// BrowserStatusResponse reports whether the desktop-owned browser transport is
// ready. A connected runtime can create the session target while its panel is
// hidden; panel visibility is intentionally not part of this state.
type BrowserStatusResponse struct {
	SessionID   domain.SessionID `json:"sessionId"`
	Connected   bool             `json:"connected"`
	ConnectedAt time.Time        `json:"connectedAt,omitempty"`
	Transport   string           `json:"transport"`
}

// PreviewServerStatusResponse reports the deterministic server Tallpine owns for one
// session. Logs are bounded to the latest lines and never contain global
// process or port discovery.
type PreviewServerStatusResponse struct {
	SessionID     domain.SessionID `json:"sessionId"`
	State         string           `json:"state" enum:"stopped,starting,ready,stopping,failed"`
	Configuration string           `json:"configuration,omitempty"`
	TargetKind    string           `json:"targetKind,omitempty" enum:"app,api"`
	URL           string           `json:"url,omitempty"`
	Port          int              `json:"port,omitempty"`
	StartedAt     time.Time        `json:"startedAt,omitempty"`
	Error         string           `json:"error,omitempty"`
	Logs          []string         `json:"logs"`
}

// RollbackSessionResponse is the body of POST /api/v1/sessions/{sessionId}/rollback.
// Exactly one of Deleted/Killed is true on a successful rollback; both are
// false when the session was already absent or already terminated (benign).
type RollbackSessionResponse struct {
	OK        bool             `json:"ok"`
	SessionID domain.SessionID `json:"sessionId"`
	Deleted   bool             `json:"deleted,omitempty"`
	Killed    bool             `json:"killed,omitempty"`
}

// SendSessionMessageRequest is the body of POST /api/v1/sessions/{sessionId}/send.
type SendSessionMessageRequest struct {
	// SenderSessionID is cooperative loopback attribution, not authentication.
	SenderSessionID string `json:"senderSessionId,omitempty"`
	Message         string `json:"message" minLength:"1" maxLength:"4096"`
	// UserAuthored marks content written directly by the user but delivered via
	// Tallpine's automation relay, such as inline document feedback.
	UserAuthored bool `json:"userAuthored,omitempty"`
	// RelayOwnerMessage names a message in the sending agent's own chat (an id,
	// or "last"). The daemon checks it is a turn the owner typed and delivers
	// it verbatim in a daemon-stamped block before Message.
	RelayOwnerMessage string `json:"relayOwnerMessage,omitempty" maxLength:"128"`
	// Attachment is an optional inline image (e.g. a browser-annotation
	// snapshot) delivered alongside the message. The daemon writes it into the
	// session worktree and appends a path reference to the message.
	Attachment *AttachmentInput `json:"attachment,omitempty"`
}

// SendSessionMessageResponse is the body of POST /api/v1/sessions/{sessionId}/send.
type SendSessionMessageResponse struct {
	OK        bool             `json:"ok"`
	SessionID domain.SessionID `json:"sessionId"`
	Message   string           `json:"message"`
	// Outcome is "delivered" when Tallpine handed the message to the agent, "queued"
	// when Tallpine accepted it but the agent will only see it after a running turn
	// or an interface transition, and "duplicate" when a message with the same
	// client message id was already accepted so nothing new was sent. Absent
	// when the daemon cannot tell.
	Outcome string `json:"outcome,omitempty" enum:"delivered,queued,duplicate"`
	// DeliveryID correlates the message (chat turn id, queued message id, or
	// session-send delivery id) when the delivery path has one.
	DeliveryID string `json:"deliveryId,omitempty"`
	// WaitingOnInput is true when the target's running Chat turn was blocked on
	// an in-chat question. A "delivered" message was steered into that turn; a
	// "queued" one waits until the question is answered.
	WaitingOnInput bool `json:"waitingOnInput,omitempty"`
}
