package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type steerDeliveryRequest struct {
	Text            string                  `json:"text"`
	Content         []ports.ChatContent     `json:"content,omitempty"`
	Origin          domain.MessageOrigin    `json:"origin"`
	SenderSessionID string                  `json:"senderSessionId,omitempty"`
	Settings        deliveryRequestSettings `json:"settings"`
}

func encodeSteerDeliveryRequest(msg ports.ChatUserMessage) (string, error) {
	encoded, err := json.Marshal(steerDeliveryRequest{
		Text: msg.Text, Content: msg.Content, Origin: normalizeOrigin(msg.Origin),
		SenderSessionID: msg.SenderSessionID,
		Settings: deliveryRequestSettings{
			Model: msg.Settings.Model, Effort: msg.Settings.Effort, Approval: msg.Settings.Approval,
		},
	})
	if err != nil {
		return "", fmt.Errorf("encode steer delivery request: %w", err)
	}
	return string(encoded), nil
}

func replaySteerDelivery(
	delivery domain.ConversationSteerDelivery,
	requestJSON string,
) (SteerResult, error) {
	if delivery.RequestJSON != requestJSON {
		return SteerResult{}, ErrSteerIdempotencyConflict
	}
	switch delivery.State {
	case domain.ConversationSteerAccepted:
		return SteerResult{
			ProviderTurnID: delivery.ProviderTurnID,
			ActivityID:     delivery.ActivityID,
		}, nil
	case domain.ConversationSteerRejected:
		return SteerResult{}, replaySteerRejection(delivery)
	case domain.ConversationSteerReserved:
		return SteerResult{}, ErrSteerDeliveryUncertain
	default:
		return SteerResult{}, fmt.Errorf("%w: invalid durable state %q",
			ErrSteerDeliveryUncertain, delivery.State)
	}
}

type storedSteerRejection struct {
	message string
	cause   error
}

func (e storedSteerRejection) Error() string { return e.message }
func (e storedSteerRejection) Unwrap() error { return e.cause }

func replaySteerRejection(delivery domain.ConversationSteerDelivery) error {
	var cause error
	switch delivery.RejectionKind {
	case domain.ConversationSteerRejectedNoActiveTurn:
		cause = ErrNoActiveTurn
	case domain.ConversationSteerRejectedUnsupported:
		cause = ErrSteerUnsupported
	case domain.ConversationSteerRejectedTurnNotSteerable:
		cause = ErrTurnNotSteerable
	case domain.ConversationSteerRejectedContentUnsupported:
		cause = ErrSteerContentUnsupported
	case domain.ConversationSteerRejectedByProvider:
		cause = ErrProviderRefused
	case domain.ConversationSteerRejectedInterfaceTransition:
		cause = ErrControllerHandoff
	default:
		return fmt.Errorf("%w: invalid durable rejection %q",
			ErrSteerDeliveryUncertain, delivery.RejectionKind)
	}
	return storedSteerRejection{message: delivery.RejectionMessage, cause: cause}
}

func classifySteerRejection(
	err error,
) (domain.ConversationSteerRejectionKind, bool, error) {
	switch {
	case errors.Is(err, ports.ErrChatNoSteerableTurn):
		// The turn ended, or was replaced, between Tallpine's check and the provider's.
		// The provider is the authority on that, and losing the race is ordinary.
		return domain.ConversationSteerRejectedNoActiveTurn, true, ErrNoActiveTurn
	case errors.Is(err, ports.ErrChatTurnNotSteerable):
		return domain.ConversationSteerRejectedTurnNotSteerable, true,
			fmt.Errorf("%w: %w", ErrTurnNotSteerable, err)
	case errors.Is(err, ports.ErrChatSteerContentUnsupported):
		return domain.ConversationSteerRejectedContentUnsupported, true,
			fmt.Errorf("%w: %w", ErrSteerContentUnsupported, err)
	}
	classified := classify(err)
	if errors.Is(classified, ErrProviderRefused) {
		return domain.ConversationSteerRejectedByProvider, true, classified
	}
	return "", false, classified
}

// recordSteer writes the guidance onto the turn that took it.
//
// It lands as an activity, not as a message, and the reason is structural rather
// than semantic: a steer joins a turn that is already running, and Tallpine's only durable
// write that can attach to a turn in flight is the activity row. AppendUserMessage
// opens a NEW turn — using it here would mint a second turn row that the drain loop
// would later dispatch as its own turn, sending the user's correction twice. The row
// is tagged `event: "steer"` in its detail so a client can render it as the user's
// own words rather than as a system notice. Cross-session CLI steers additionally
// carry sender metadata so the client can attribute them as automation without
// changing the activity storage model.
//
// The provider's own echo is not the record. It replays the guidance as a
// `userMessage` item on the turn, but the driver drops those: Tallpine records what the
// user said when it accepts the request, and re-emitting the echo would show it
// twice.
func (c *Controller) recordSteer(
	ctx context.Context,
	providerTurnID string,
	msg ports.ChatUserMessage,
	providerInput steerProviderInput,
) (string, error) {
	id := c.newID()
	activity, err := makeSteerActivity(id, msg, "", providerInput)
	if err != nil {
		return "", err
	}
	if err := c.store.UpsertActivity(ctx, c.conversation.ID, providerTurnID,
		activity, c.now()); err != nil {
		return "", fmt.Errorf("record steer on turn %s: %w", providerTurnID, err)
	}
	return id, nil
}

type steerProviderInput struct {
	Text          string
	Content       []ports.ChatContent
	SourceTurnIDs []string
	StoredOnTurn  bool
}

func makeSteerActivity(
	id string,
	msg ports.ChatUserMessage,
	sourceTurnID string,
	providerInputs ...steerProviderInput,
) (domain.ConversationActivity, error) {
	detail := map[string]any{
		"event":  "steer",
		"text":   msg.Text,
		"origin": string(normalizeOrigin(msg.Origin)),
	}
	if msg.SenderSessionID != "" {
		detail["senderSessionId"] = msg.SenderSessionID
		if msg.SenderProjectID != "" {
			detail["senderProjectId"] = msg.SenderProjectID
		}
		if msg.SenderDisplayName != "" {
			detail["senderDisplayName"] = msg.SenderDisplayName
		}
	}
	if msg.ClientMessageID != "" {
		detail["clientMessageId"] = msg.ClientMessageID
	}
	if sourceTurnID != "" {
		detail["sourceTurnId"] = sourceTurnID
	}
	if len(providerInputs) > 0 {
		detail["providerInputCaptured"] = true
		detail["providerInputText"] = providerInputs[0].Text
		detail["providerInputContent"] = providerInputs[0].Content
		detail["providerInputSourceTurnIds"] = providerInputs[0].SourceTurnIDs
		detail["providerInputStoredOnTurn"] = providerInputs[0].StoredOnTurn
	}
	if len(msg.Content) > 0 {
		detail["content"] = msg.Content
	}
	encoded, err := json.Marshal(detail)
	if err != nil {
		return domain.ConversationActivity{}, fmt.Errorf("encode steer detail: %w", err)
	}
	return domain.ConversationActivity{
		ID:     id,
		Kind:   domain.ActivityKindSystem,
		Status: domain.ActivityStatusCompleted,
		// Delivered, not pending: the provider accepted it before this ran.
		Summary: steerSummary(msg.Text),
		Detail:  encoded,
		// A synthetic item key, so a client retrying with the same idempotency
		// handle updates this row instead of adding a second one. Prefixed because
		// this id is Tallpine's, not the provider's, and the two share a column.
		ProviderItemID: steerItemID(msg.ClientMessageID),
	}, nil
}

// steerItemID is the dedupe key for a recorded steer, or empty when the caller
// supplied no idempotency handle and every request is a distinct piece of guidance.
func steerItemID(clientMessageID string) string {
	if clientMessageID == "" {
		return ""
	}
	return "steer:" + clientMessageID
}

// steerSummary reduces guidance to a one-line label. The full text stays in the
// detail payload, so this only decides what a collapsed row reads like.
func steerSummary(text string) string {
	collapsed := strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(collapsed) <= maxSteerSummaryRunes {
		return collapsed
	}
	runes := []rune(collapsed)
	return strings.TrimRight(string(runes[:maxSteerSummaryRunes]), " ") + "…"
}
