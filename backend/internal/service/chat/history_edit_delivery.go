package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// sendEditedMessage always attaches the attempted replacement to its branch. A
// transport error is ambiguous: the provider may have accepted the prompt before
// the connection failed, so deleting and blindly retrying it could execute the
// work twice. Keeping the failed turn makes that uncertainty durable and leaves
// both alternatives navigable after restart. A provider-declared refusal is
// conclusive, so Tallpine safely returns the user to the source branch.
func (s *Service) sendEditedMessage(
	ctx context.Context,
	sourceBranchID, activeBranchID string,
	controller *Controller,
	msg ports.ChatUserMessage,
	restoreConclusiveFailure bool,
) (EditMessageResult, error) {
	turn, sendErr := controller.Send(ctx, msg)
	result := EditMessageResult{
		SourceBranchID: sourceBranchID,
		ActiveBranchID: activeBranchID,
		Turn:           turn,
	}
	if turn.ID == "" {
		// Nothing durable was created, so the provider was never dispatched. This
		// includes a duplicate client-message id: treating the controller's empty
		// no-op result as a successful edit would leave an invisible active child.
		if sendErr == nil {
			sendErr = errors.New("edited message was not dispatched")
		}
		if restoreConclusiveFailure {
			if _, err := s.activateBranchLocked(ctx, controller.sessionID, sourceBranchID); err != nil {
				sendErr = errors.Join(sendErr, fmt.Errorf("restore source after undispatched edit: %w", err))
			}
		}
		return s.rejectEditDelivery(
			ctx, controller.conversation.ID, msg.ClientMessageID, result, classify(sendErr))
	}
	if sendErr != nil {
		if err := s.store.UpdateConversationBranchReplacement(ctx, activeBranchID, turn.ID); err != nil {
			sendErr = errors.Join(sendErr, fmt.Errorf("record failed edited turn: %w", err))
		}
		var refusal providerRefusal
		if restoreConclusiveFailure && errors.As(sendErr, &refusal) && refusal.ChatRefusal() {
			if _, err := s.activateBranchLocked(ctx, controller.sessionID, sourceBranchID); err != nil {
				sendErr = errors.Join(sendErr, fmt.Errorf("restore source after refused edit: %w", err))
			}
		}
		return s.rejectEditDelivery(
			ctx, controller.conversation.ID, msg.ClientMessageID, result, classify(sendErr))
	}
	if msg.ClientMessageID != "" {
		if err := s.store.CompleteEditDelivery(
			context.WithoutCancel(ctx), controller.conversation.ID, msg.ClientMessageID,
			sourceBranchID, activeBranchID, turn, s.now()); err != nil {
			return result, fmt.Errorf("%w: complete delivery: %w", ErrEditDeliveryUncertain, err)
		}
	} else if err := s.store.UpdateConversationBranchReplacement(ctx, activeBranchID, turn.ID); err != nil {
		return result, err
	}
	return result, nil
}

// EditMessageResult identifies the source, selected child, and replacement turn.
type EditMessageResult struct {
	SourceBranchID string
	ActiveBranchID string
	Turn           domain.ConversationTurn
}

type editDeliveryRequest struct {
	SourceTurnID string                  `json:"sourceTurnId"`
	Text         string                  `json:"text"`
	Content      []ports.ChatContent     `json:"content,omitempty"`
	Origin       domain.MessageOrigin    `json:"origin"`
	Settings     deliveryRequestSettings `json:"settings"`
}

func encodeEditDeliveryRequest(turnID string, msg ports.ChatUserMessage) (string, error) {
	encoded, err := json.Marshal(editDeliveryRequest{
		SourceTurnID: turnID, Text: msg.Text, Content: msg.Content,
		Origin: normalizeOrigin(msg.Origin),
		Settings: deliveryRequestSettings{
			Model: msg.Settings.Model, Effort: msg.Settings.Effort, Approval: msg.Settings.Approval,
		},
	})
	if err != nil {
		return "", fmt.Errorf("encode edit delivery request: %w", err)
	}
	return string(encoded), nil
}

func replayEditDelivery(
	delivery domain.ConversationEditDelivery,
	requestJSON string,
) (EditMessageResult, error) {
	if delivery.RequestJSON != requestJSON {
		return EditMessageResult{}, ErrEditIdempotencyConflict
	}
	switch delivery.State {
	case domain.ConversationEditAccepted:
		return EditMessageResult{
			SourceBranchID: delivery.SourceBranchID,
			ActiveBranchID: delivery.ActiveBranchID,
			Turn:           delivery.Turn,
		}, nil
	case domain.ConversationEditRejected:
		return EditMessageResult{}, replayEditRejection(delivery)
	case domain.ConversationEditReserved:
		return EditMessageResult{}, ErrEditDeliveryUncertain
	default:
		return EditMessageResult{}, fmt.Errorf("%w: invalid durable state %q",
			ErrEditDeliveryUncertain, delivery.State)
	}
}

type storedEditRejection struct {
	message string
	cause   error
}

func (e storedEditRejection) Error() string { return e.message }
func (e storedEditRejection) Unwrap() error { return e.cause }

func replayEditRejection(delivery domain.ConversationEditDelivery) error {
	var cause error
	switch delivery.RejectionKind {
	case domain.ConversationEditRejectedMissingTurn:
		cause = errors.Join(ErrEditTurnInvalid, domain.ErrNoConversationTurn)
	case domain.ConversationEditRejectedInvalid:
		cause = ErrEditTurnInvalid
	case domain.ConversationEditRejectedUnsupported:
		cause = ErrForkUnsupported
	case domain.ConversationEditRejectedBusy:
		cause = ErrTurnRunning
	case domain.ConversationEditRejectedInterfaceTransition:
		cause = ErrControllerHandoff
	case domain.ConversationEditRejectedByProvider:
		cause = ErrProviderRefused
	case domain.ConversationEditRejectedProviderFailure:
		cause = ErrEditDeliveryRejected
	default:
		return fmt.Errorf("%w: invalid durable rejection %q",
			ErrEditDeliveryUncertain, delivery.RejectionKind)
	}
	return storedEditRejection{message: delivery.RejectionMessage, cause: cause}
}

// classifyEditRejection separates facts that prove no provider delivery occurred
// from generic errors whose delivery outcome the Controller.Send contract cannot
// establish. Only the former may settle a reservation as rejected.
func classifyEditRejection(
	err error,
) (domain.ConversationEditRejectionKind, bool, error) {
	err = classify(err)
	switch {
	case errors.Is(err, domain.ErrNoConversationTurn):
		return domain.ConversationEditRejectedMissingTurn, true, err
	case errors.Is(err, ErrEditTurnInvalid):
		return domain.ConversationEditRejectedInvalid, true, err
	case errors.Is(err, ErrForkUnsupported):
		return domain.ConversationEditRejectedUnsupported, true, err
	case errors.Is(err, ErrControllerHandoff):
		return domain.ConversationEditRejectedInterfaceTransition, true, err
	case errors.Is(err, ErrTurnRunning):
		return domain.ConversationEditRejectedBusy, true, err
	case errors.Is(err, ErrProviderRefused):
		return domain.ConversationEditRejectedByProvider, true, err
	default:
		return "", false, err
	}
}

func (s *Service) rejectEditDelivery(
	ctx context.Context,
	conversationID, clientMessageID string,
	result EditMessageResult,
	cause error,
) (EditMessageResult, error) {
	if clientMessageID == "" {
		return result, cause
	}
	kind, definitive, classified := classifyEditRejection(cause)
	if !definitive {
		return result, fmt.Errorf("%w: %w", ErrEditDeliveryUncertain, classified)
	}
	return s.persistRejectedEditDelivery(
		ctx, conversationID, clientMessageID, result, kind, classified)
}

// rejectUndispatchedEditDelivery settles a reserved edit after local preparation
// proves that the edited prompt never reached Controller.Send. Generic provider
// and transport errors after Send remain uncertain; local replay/capability
// failures are safe to record as rejected so the same delivery ID cannot become
// permanently reserved after a lost response or restart.
func (s *Service) rejectUndispatchedEditDelivery(
	ctx context.Context,
	conversationID, clientMessageID string,
	cause error,
) (EditMessageResult, error) {
	if clientMessageID == "" {
		return EditMessageResult{}, cause
	}
	kind, definitive, classified := classifyEditRejection(cause)
	if !definitive {
		kind = domain.ConversationEditRejectedProviderFailure
		diagnostic := classify(cause)
		classified = storedEditRejection{
			message: diagnostic.Error(),
			cause:   ErrEditDeliveryRejected,
		}
	}
	return s.persistRejectedEditDelivery(
		ctx, conversationID, clientMessageID, EditMessageResult{}, kind, classified)
}

func (s *Service) persistRejectedEditDelivery(
	ctx context.Context,
	conversationID, clientMessageID string,
	result EditMessageResult,
	kind domain.ConversationEditRejectionKind,
	cause error,
) (EditMessageResult, error) {
	if err := s.store.RejectEditDelivery(
		context.WithoutCancel(ctx), conversationID, clientMessageID,
		kind, cause.Error(), s.now()); err != nil {
		return result, fmt.Errorf("%w: persist rejected result: %w",
			ErrEditDeliveryUncertain, err)
	}
	return result, cause
}

// Explicit JSON names freeze the durable request identity independently of the
// provider port's Go field names. A later refactor must not turn a safe retry into
// an idempotency conflict after an app upgrade.
type deliveryRequestSettings struct {
	Model    string               `json:"model,omitempty"`
	Effort   string               `json:"effort,omitempty"`
	Approval ports.PermissionMode `json:"approval,omitempty"`
}
