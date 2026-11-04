package domain

import (
	"time"
)

// ConversationQueuedEditDelivery identifies one exact queued-message mutation.
// Only its digest is stored; uploaded image bytes remain with the message.
type ConversationQueuedEditDelivery struct {
	ClientMessageID string
	RequestHash     string
}

// ConversationEditDelivery is Tallpine's durable answer to one caller-owned inline
// edit handle. A reservation with ProviderWorkStarted set cannot be dispatched
// again without proof of its outcome. Earlier reservations can resume safely.
// Accepted and rejected results replay across branch changes and daemon restarts.
type ConversationEditDelivery struct {
	ConversationID      string
	ClientMessageID     string
	RequestJSON         string
	ProviderWorkStarted bool
	State               ConversationEditDeliveryState
	SourceBranchID      string
	ActiveBranchID      string
	Turn                ConversationTurn
	RejectionKind       ConversationEditRejectionKind
	RejectionMessage    string
	CreatedAt           time.Time
	SettledAt           *time.Time
}

// ConversationEditDeliveryState records whether a reserved edit was accepted
// or definitively rejected.
type ConversationEditDeliveryState string

// Conversation edit delivery states.
const (
	ConversationEditReserved ConversationEditDeliveryState = "reserved"
	ConversationEditAccepted ConversationEditDeliveryState = "accepted"
	ConversationEditRejected ConversationEditDeliveryState = "rejected"
)

// ConversationEditRejectionKind reconstructs errors.Is behavior for definitive
// edit failures after the controller that observed them no longer exists.
type ConversationEditRejectionKind string

// Conversation edit rejection kinds.
const (
	ConversationEditRejectedInvalid             ConversationEditRejectionKind = "invalid_turn"
	ConversationEditRejectedMissingTurn         ConversationEditRejectionKind = "missing_turn"
	ConversationEditRejectedUnsupported         ConversationEditRejectionKind = "unsupported"
	ConversationEditRejectedBusy                ConversationEditRejectionKind = "busy"
	ConversationEditRejectedInterfaceTransition ConversationEditRejectionKind = "interface_transition"
	ConversationEditRejectedByProvider          ConversationEditRejectionKind = "provider_refused"
	// ConversationEditRejectedProviderFailure records a generic local preparation
	// failure when Tallpine can prove provider dispatch never occurred. It also replays
	// reservations settled by older builds that treated generic provider/transport
	// errors as definitive.
	ConversationEditRejectedProviderFailure ConversationEditRejectionKind = "provider_failure"
)

// ConversationSteerDelivery is Tallpine's durable answer to one idempotent steer.
// Reserved is deliberately terminal from an automatic-retry perspective: once
// provider I/O may have begun, only a recorded accepted or rejected result can
// safely unlock the same client handle.
type ConversationSteerDelivery struct {
	ConversationID   string
	ClientMessageID  string
	RequestJSON      string
	State            ConversationSteerDeliveryState
	ProviderTurnID   string
	ActivityID       string
	RejectionKind    ConversationSteerRejectionKind
	RejectionMessage string
	CreatedAt        time.Time
	SettledAt        *time.Time
}

// ConversationSteerDeliveryState records whether a reserved steer was accepted
// or definitively rejected.
type ConversationSteerDeliveryState string

// Conversation steer delivery states.
const (
	ConversationSteerReserved ConversationSteerDeliveryState = "reserved"
	ConversationSteerAccepted ConversationSteerDeliveryState = "accepted"
	ConversationSteerRejected ConversationSteerDeliveryState = "rejected"
)

// ConversationSteerRejectionKind is the stable typed outcome persisted after a
// provider definitively declines a steer. The original message is display detail;
// this discriminator is what reconstructs errors.Is behavior after restart.
type ConversationSteerRejectionKind string

// Conversation steer rejection kinds.
const (
	ConversationSteerRejectedNoActiveTurn        ConversationSteerRejectionKind = "no_active_turn"
	ConversationSteerRejectedUnsupported         ConversationSteerRejectionKind = "unsupported"
	ConversationSteerRejectedTurnNotSteerable    ConversationSteerRejectionKind = "turn_not_steerable"
	ConversationSteerRejectedContentUnsupported  ConversationSteerRejectionKind = "content_unsupported"
	ConversationSteerRejectedByProvider          ConversationSteerRejectionKind = "provider_refused"
	ConversationSteerRejectedInterfaceTransition ConversationSteerRejectionKind = "interface_transition"
)
