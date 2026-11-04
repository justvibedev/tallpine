package chat

import (
	"context"
	"errors"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// Steer hands guidance to the provider for the turn currently in flight, then
// records it on that turn.
//
// The wait for acknowledgement is load-bearing, not defensive. Codex refuses a
// steer for a turn it has accepted but not yet announced — probed directly: a steer
// sent immediately after turn/start returned, carrying the id turn/start itself had
// just handed back, came back "no active turn to steer". Steering is most useful in
// exactly that window, the second after someone realizes they sent the wrong thing,
// so the same helper Interrupt uses to ride out that gap is used here.
//
// Tallpine durably reserves a caller-supplied client handle before provider I/O. A
// definitive refusal settles that handle as rejected, success records the visible
// activity and accepted result atomically, and any gap between those facts remains
// uncertain forever. That fail-closed state is what makes a retry unable to deliver
// the same guidance twice even when the provider ignores the client handle.
func (c *Controller) Steer(ctx context.Context, msg ports.ChatUserMessage) (SteerResult, error) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	return c.steerLocked(ctx, msg)
}

func (c *Controller) steerLocked(ctx context.Context, msg ports.ChatUserMessage) (SteerResult, error) {

	requestJSON, err := encodeSteerDeliveryRequest(msg)
	if err != nil {
		return SteerResult{}, err
	}
	if msg.ClientMessageID != "" {
		delivery, found, loadErr := c.store.SteerDelivery(
			ctx, c.conversation.ID, msg.ClientMessageID)
		if loadErr != nil {
			return SteerResult{}, fmt.Errorf("%w: load prior result: %w",
				ErrSteerDeliveryUncertain, loadErr)
		}
		if found {
			return replaySteerDelivery(delivery, requestJSON)
		}
	}
	if c.handoffActive() {
		return c.rejectSteerBeforeDispatch(ctx, msg.ClientMessageID, requestJSON,
			durableSteerRefusal{
				kind:           domain.ConversationSteerRejectedInterfaceTransition,
				cause:          ErrControllerHandoff,
				reserveContext: "reserve interface-transition refusal",
				persistContext: "persist interface-transition refusal",
			})
	}
	steerer, ok := c.steerer()
	if !ok {
		return c.rejectSteerBeforeDispatch(ctx, msg.ClientMessageID, requestJSON,
			durableSteerRefusal{
				kind:           domain.ConversationSteerRejectedUnsupported,
				cause:          ErrSteerUnsupported,
				reserveContext: "reserve unsupported result",
				persistContext: "persist unsupported result",
			})
	}

	turn, ok := c.awaitAcknowledgedTurn(ctx)
	if !ok {
		// Nothing is in flight. Reusing the interrupt sentinel keeps one code for
		// "there is no turn" across every command that needs one; the endpoint says
		// what to do about it.
		return c.rejectSteerBeforeDispatch(ctx, msg.ClientMessageID, requestJSON,
			durableSteerRefusal{
				kind:           domain.ConversationSteerRejectedNoActiveTurn,
				cause:          ErrNoActiveTurn,
				reserveContext: "reserve refusal",
				persistContext: "persist no-active-turn refusal",
			})
	}
	if msg.ClientMessageID != "" {
		delivery, created, reserveErr := c.store.ReserveSteerDelivery(
			ctx, c.conversation.ID, msg.ClientMessageID, requestJSON, c.now())
		if reserveErr != nil {
			return SteerResult{}, fmt.Errorf("%w: reserve delivery: %w",
				ErrSteerDeliveryUncertain, reserveErr)
		}
		if !created {
			return replaySteerDelivery(delivery, requestJSON)
		}
	}
	batch, err := c.store.ClaimQueuedBatch(ctx, c.conversation.ID, false, 0, c.now())
	if errors.Is(err, domain.ErrNoQueuedTurn) {
		batch = nil
	} else if err != nil {
		return SteerResult{}, fmt.Errorf("%w: claim pending messages: %w", ErrSteerDeliveryUncertain, err)
	}
	providerMessage := msg
	if len(batch) > 0 {
		providerMessage, err = c.queuedBatchInput(ctx, batch, &msg)
		if err != nil {
			return SteerResult{}, c.rejectInvalidSteerBatch(ctx, msg, batch, err)
		}
	}

	ref, err := steerer.Steer(ctx, turn, providerMessage)
	if err != nil {
		return SteerResult{}, c.settleSteerFailure(ctx, msg, batch, turn, err)
	}

	landed := ref.ProviderTurnID
	if landed == "" {
		landed = turn
	}
	providerInput := steerProviderInput{
		Text: providerMessage.Text, Content: providerMessage.Content,
		SourceTurnIDs: queuedBatchIDs(batch),
	}
	if len(batch) > 0 {
		settleCtx := context.WithoutCancel(ctx)
		target, err := c.store.TurnByProviderID(settleCtx, c.conversation.ID, landed)
		if err == nil && target.ProviderInputText == "" {
			err = c.store.RecordProviderInput(settleCtx, target.ID, providerMessage.Text)
			providerInput.StoredOnTurn = err == nil
		}
		if err != nil {
			if failErr := c.store.FailQueuedBatch(context.WithoutCancel(ctx), c.conversation.ID,
				queuedBatchIDs(batch), ErrSteerDeliveryUncertain.Error(), c.now()); failErr != nil {
				c.log.Error("failed to settle steered batch with unrecorded provider input", "error", failErr)
			}
			return SteerResult{ProviderTurnID: landed}, fmt.Errorf("%w: record steered batch provider input: %w", ErrSteerDeliveryUncertain, err)
		}
	}
	if msg.ClientMessageID == "" {
		activityID, recordErr := c.recordSteer(ctx, landed, msg, providerInput)
		if recordErr != nil {
			if len(batch) > 0 {
				_ = c.store.FailQueuedBatch(context.WithoutCancel(ctx), c.conversation.ID,
					queuedBatchIDs(batch), ErrSteerDeliveryUncertain.Error(), c.now())
			}
			// The guidance IS with the agent; only Tallpine's record of it failed. Reporting the
			// error rather than swallowing it, because a steer the timeline never mentions
			// is a conversation whose next answer has no visible cause.
			return SteerResult{ProviderTurnID: landed}, recordErr
		}
		if len(batch) > 0 {
			if err := c.store.CompleteQueuedBatch(context.WithoutCancel(ctx), c.conversation.ID, queuedBatchIDs(batch), landed, domain.DeliveryModeSteered, c.now()); err != nil {
				_ = c.store.FailQueuedBatch(context.WithoutCancel(ctx), c.conversation.ID,
					queuedBatchIDs(batch), ErrSteerDeliveryUncertain.Error(), c.now())
				return SteerResult{ProviderTurnID: landed, ActivityID: activityID}, fmt.Errorf("%w: complete pending messages: %w", ErrSteerDeliveryUncertain, err)
			}
		}
		return SteerResult{ProviderTurnID: landed, ActivityID: activityID}, nil
	}
	activityID := c.newID()
	activity, err := makeSteerActivity(activityID, msg, "", providerInput)
	if err != nil {
		return SteerResult{}, fmt.Errorf("%w: %w", ErrSteerDeliveryUncertain, err)
	}
	var completeErr error
	if len(batch) == 0 {
		completeErr = c.store.CompleteSteerDelivery(context.WithoutCancel(ctx), c.conversation.ID,
			msg.ClientMessageID, landed, activity, c.now())
	} else {
		completeErr = c.store.CompleteSteerBatchDelivery(context.WithoutCancel(ctx), c.conversation.ID,
			msg.ClientMessageID, landed, activity, queuedBatchIDs(batch), c.now())
	}
	if completeErr != nil {
		if len(batch) > 0 {
			_ = c.store.FailQueuedBatch(context.WithoutCancel(ctx), c.conversation.ID,
				queuedBatchIDs(batch), ErrSteerDeliveryUncertain.Error(), c.now())
		}
		return SteerResult{}, fmt.Errorf("%w: %w", ErrSteerDeliveryUncertain, completeErr)
	}
	return SteerResult{ProviderTurnID: landed, ActivityID: activityID}, nil
}

// SteerOrSend selects and persists one delivery outcome while holding the same
// lock used by ordinary sends and steering.
func (c *Controller) SteerOrSend(
	ctx context.Context,
	msg ports.ChatUserMessage,
	recoverOnly bool,
) (SteerOrSendResult, error) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()

	if existing, found, err := c.store.ConversationMessageByClientID(
		ctx, c.conversation.ID, msg.ClientMessageID,
	); err != nil {
		return SteerOrSendResult{}, fmt.Errorf("recover sent message: %w", err)
	} else if found {
		if !recoverOnly {
			hash, err := clientPayloadHash(msg)
			if err != nil {
				return SteerOrSendResult{}, err
			}
			if existing.ClientPayloadHash != "" {
				if existing.ClientPayloadHash != hash {
					return SteerOrSendResult{}, domain.ErrClientMessageConflict
				}
			} else if !legacyMessageMatches(existing, msg) {
				return SteerOrSendResult{}, domain.ErrClientMessageConflict
			}
		}
		turn, err := c.store.TurnByID(ctx, existing.TurnID)
		if err != nil {
			return SteerOrSendResult{}, fmt.Errorf("recover sent turn: %w", err)
		}
		return SteerOrSendResult{Duplicate: true, Turn: turn}, nil
	}

	requestJSON, err := encodeSteerDeliveryRequest(msg)
	if err != nil {
		return SteerOrSendResult{}, err
	}
	if delivery, found, err := c.store.SteerDelivery(
		ctx, c.conversation.ID, msg.ClientMessageID,
	); err != nil {
		return SteerOrSendResult{}, fmt.Errorf("%w: load prior result: %w", ErrSteerDeliveryUncertain, err)
	} else if found {
		replayRequest := requestJSON
		if recoverOnly {
			replayRequest = delivery.RequestJSON
		}
		steered, replayErr := replaySteerDelivery(delivery, replayRequest)
		if replayErr == nil {
			return SteerOrSendResult{Steered: true, Duplicate: true, Steer: steered}, nil
		}
		if !errors.Is(replayErr, ErrNoActiveTurn) || recoverOnly {
			return SteerOrSendResult{}, replayErr
		}
	}
	if recoverOnly {
		return SteerOrSendResult{}, ErrSteerDeliveryUncertain
	}

	if _, active := c.awaitAcknowledgedTurn(ctx); active {
		steered, err := c.steerLocked(ctx, msg)
		if err == nil {
			return SteerOrSendResult{Steered: true, Steer: steered}, nil
		}
		if !errors.Is(err, ErrNoActiveTurn) {
			return SteerOrSendResult{}, err
		}
	}

	// Compaction can own the provider before it has a turn ID to steer.
	// Preserve the prompt in order until that boundary settles: it queues behind
	// whatever is already pending, so no batch is claimed here.
	c.mu.Lock()
	compacting := c.compactionPending
	c.mu.Unlock()
	var batch []domain.QueuedTurn
	if !compacting {
		var batchErr error
		batch, batchErr = c.store.ClaimQueuedBatch(ctx, c.conversation.ID, false, 0, c.now())
		if errors.Is(batchErr, domain.ErrNoQueuedTurn) {
			batch = nil
		} else if batchErr != nil {
			return SteerOrSendResult{}, batchErr
		}
	}
	var override *ports.ChatUserMessage
	if len(batch) > 0 {
		combined, composeErr := c.queuedBatchInput(ctx, batch, &msg)
		if composeErr != nil {
			_ = c.store.ReleaseQueuedBatch(context.WithoutCancel(ctx), c.conversation.ID, queuedBatchIDs(batch))
			return SteerOrSendResult{}, composeErr
		}
		override = &combined
	}
	turn, err := c.sendLocked(ctx, msg, compacting, override, queuedBatchIDs(batch))
	if err != nil {
		// sendLocked and dispatch settle the claimed batch themselves: released
		// when nothing reached the provider, failed only when delivery is uncertain.
		return SteerOrSendResult{}, err
	}
	if turn.ID == "" && len(batch) > 0 {
		if err := c.store.ReleaseQueuedBatch(context.WithoutCancel(ctx), c.conversation.ID, queuedBatchIDs(batch)); err != nil {
			return SteerOrSendResult{}, fmt.Errorf("release batch after duplicate send: %w", err)
		}
	}
	return SteerOrSendResult{Turn: turn}, nil
}

// rejectInvalidSteerBatch releases the claimed batch and records the refusal of
// a batch whose content the provider cannot take; it returns err, or the error
// that kept the refusal from being recorded.
func (c *Controller) rejectInvalidSteerBatch(ctx context.Context, msg ports.ChatUserMessage, batch []domain.QueuedTurn, err error) error {
	if releaseErr := c.store.ReleaseQueuedBatch(context.WithoutCancel(ctx), c.conversation.ID, queuedBatchIDs(batch)); releaseErr != nil {
		return errors.Join(err, releaseErr)
	}
	if msg.ClientMessageID != "" {
		if rejectErr := c.store.RejectSteerDelivery(context.WithoutCancel(ctx), c.conversation.ID,
			msg.ClientMessageID, domain.ConversationSteerRejectedContentUnsupported, err.Error(), c.now()); rejectErr != nil {
			return fmt.Errorf("%w: reject invalid batch: %w", ErrSteerDeliveryUncertain, rejectErr)
		}
	}
	return err
}

// settleSteerFailure releases or fails the claimed batch after Steer returned
// an error and returns the error to report.
func (c *Controller) settleSteerFailure(ctx context.Context, msg ports.ChatUserMessage, batch []domain.QueuedTurn, turn string, err error) error {
	kind, definitive, refused := classifySteerRejection(err)
	if definitive {
		if len(batch) > 0 {
			if releaseErr := c.store.ReleaseQueuedBatch(context.WithoutCancel(ctx), c.conversation.ID, queuedBatchIDs(batch)); releaseErr != nil {
				return fmt.Errorf("%w: release refused batch: %w", ErrSteerDeliveryUncertain, releaseErr)
			}
		}
		if msg.ClientMessageID != "" {
			if rejectErr := c.store.RejectSteerDelivery(
				context.WithoutCancel(ctx), c.conversation.ID, msg.ClientMessageID,
				kind, refused.Error(), c.now()); rejectErr != nil {
				return fmt.Errorf("%w: persist provider refusal: %w",
					ErrSteerDeliveryUncertain, rejectErr)
			}
		}
		return refused
	}
	wrapped := classify(fmt.Errorf("steer turn %s: %w", turn, err))
	if len(batch) > 0 {
		if failErr := c.store.FailQueuedBatch(context.WithoutCancel(ctx), c.conversation.ID,
			queuedBatchIDs(batch), ErrSteerDeliveryUncertain.Error(), c.now()); failErr != nil {
			c.log.Error("failed to settle uncertain steered batch", "error", failErr)
		}
	}
	if msg.ClientMessageID != "" {
		return fmt.Errorf("%w: %w", ErrSteerDeliveryUncertain, wrapped)
	}
	return fmt.Errorf("%w: %w", errSteerProviderCallFailed, wrapped)
}
