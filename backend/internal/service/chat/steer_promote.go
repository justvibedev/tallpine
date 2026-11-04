package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// PromoteQueuedTurn reserves a durable queue item, asks the provider to absorb
// it, then records the destination and retires the source as one store operation.
func (c *Controller) PromoteQueuedTurn(
	ctx context.Context,
	turnID string,
) (PromoteQueuedTurnResult, error) {
	steerer, ok := c.steerer()
	if !ok {
		return PromoteQueuedTurnResult{}, ErrSteerUnsupported
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if c.handoffActive() {
		return PromoteQueuedTurnResult{}, ErrControllerHandoff
	}

	source, err := c.store.TurnByID(ctx, turnID)
	if err != nil {
		return PromoteQueuedTurnResult{}, err
	}
	if source.ConversationID != c.conversation.ID {
		return PromoteQueuedTurnResult{}, fmt.Errorf("%w: %s", domain.ErrNoConversationTurn, turnID)
	}
	if source.State != domain.TurnStateQueued {
		return PromoteQueuedTurnResult{}, fmt.Errorf("%w: %s", ErrTurnNotQueued, turnID)
	}

	target, ok := c.awaitAcknowledgedTurn(ctx)
	if !ok {
		return PromoteQueuedTurnResult{}, ErrNoActiveTurn
	}
	queued, err := c.store.ReserveQueuedTurnForPromotion(ctx, c.conversation.ID, turnID, c.now())
	if err != nil {
		return PromoteQueuedTurnResult{}, fmt.Errorf("%w: %s: %w", ErrTurnNotQueued, turnID, err)
	}
	release := func() {
		if releaseErr := c.store.ReleaseQueuedTurnPromotion(
			context.WithoutCancel(ctx), c.conversation.ID, turnID); releaseErr != nil {
			c.log.Error("failed to release queued turn promotion", "turn", turnID, "error", releaseErr)
		}
	}
	if queued.Origin != domain.MessageOriginHuman {
		release()
		return PromoteQueuedTurnResult{}, fmt.Errorf("%w: %s", ErrTurnNotQueued, turnID)
	}
	settleUncertain := func(cause error) error {
		// A transport error commonly cancels the request context after the provider
		// may already have accepted the steer. The durable safety transition must
		// survive that cancellation or the source remains visibly queued forever.
		settleCtx := context.WithoutCancel(ctx)
		if settleErr := c.store.SettleTurnByID(
			settleCtx, turnID, domain.TurnStateFailed, ErrPromotionUncertain.Error(), c.now()); settleErr != nil {
			c.log.Error("failed to settle uncertain queued turn promotion",
				"turn", turnID, "cause", cause, "error", settleErr)
		}
		return fmt.Errorf("%w: %w", ErrPromotionUncertain, cause)
	}

	var content []ports.ChatContent
	if queued.DeliveryContentJSON != "" {
		if err := json.Unmarshal([]byte(queued.DeliveryContentJSON), &content); err != nil {
			release()
			return PromoteQueuedTurnResult{}, fmt.Errorf("decode queued chat content: %w", err)
		}
	}
	msg := ports.ChatUserMessage{
		Text: queued.Text, Content: content, Origin: queued.Origin,
		ClientMessageID: queued.ClientMessageID,
	}
	ref, err := steerer.Steer(ctx, target, msg)
	if err != nil {
		classified := classify(err)
		switch {
		case errors.Is(err, ports.ErrChatNoSteerableTurn):
			release()
			return PromoteQueuedTurnResult{}, ErrNoActiveTurn
		case errors.Is(err, ports.ErrChatTurnNotSteerable):
			release()
			return PromoteQueuedTurnResult{}, fmt.Errorf("%w: %w", ErrTurnNotSteerable, err)
		case errors.Is(err, ports.ErrChatSteerContentUnsupported):
			release()
			return PromoteQueuedTurnResult{}, fmt.Errorf("%w: %w", ErrSteerContentUnsupported, err)
		case errors.Is(classified, ErrProviderRefused):
			release()
			return PromoteQueuedTurnResult{}, classified
		default:
			return PromoteQueuedTurnResult{}, settleUncertain(
				fmt.Errorf("steer queued turn %s: %w", turnID, err))
		}
	}
	landed := ref.ProviderTurnID
	if landed == "" {
		landed = target
	}
	activityID := c.newID()
	activity, err := makeSteerActivity(activityID, msg, turnID, steerProviderInput{
		Text: msg.Text, Content: msg.Content, SourceTurnIDs: []string{turnID},
	})
	if err != nil {
		return PromoteQueuedTurnResult{}, settleUncertain(err)
	}
	if err := c.store.CompleteQueuedTurnPromotion(
		ctx, c.conversation.ID, turnID, landed, activity, c.now()); err != nil {
		return PromoteQueuedTurnResult{}, settleUncertain(err)
	}
	return PromoteQueuedTurnResult{
		SourceTurnID: turnID, ProviderTurnID: landed, ActivityID: activityID,
	}, nil
}

type durableSteerRefusal struct {
	kind           domain.ConversationSteerRejectionKind
	cause          error
	reserveContext string
	persistContext string
}

func (c *Controller) rejectSteerBeforeDispatch(
	ctx context.Context,
	clientMessageID, requestJSON string,
	refusal durableSteerRefusal,
) (SteerResult, error) {
	if clientMessageID == "" {
		return SteerResult{}, refusal.cause
	}
	delivery, created, err := c.store.ReserveSteerDelivery(
		ctx, c.conversation.ID, clientMessageID, requestJSON, c.now())
	if err != nil {
		return SteerResult{}, fmt.Errorf("%w: %s: %w",
			ErrSteerDeliveryUncertain, refusal.reserveContext, err)
	}
	if !created {
		return replaySteerDelivery(delivery, requestJSON)
	}
	if err := c.store.RejectSteerDelivery(
		context.WithoutCancel(ctx), c.conversation.ID, clientMessageID,
		refusal.kind, refusal.cause.Error(), c.now()); err != nil {
		return SteerResult{}, fmt.Errorf("%w: %s: %w",
			ErrSteerDeliveryUncertain, refusal.persistContext, err)
	}
	return SteerResult{}, refusal.cause
}
