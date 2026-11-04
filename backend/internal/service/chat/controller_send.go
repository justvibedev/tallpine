package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// Send records a message and dispatches it, or queues it if the agent is busy.
//
// The durable record is written first: if the provider call then fails, the user
// can see their message and its delivery state rather than having it vanish. A
// retry carrying the same client message id is a no-op, so a flaky client cannot
// produce two provider turns.
//
// A message that arrives mid-turn stays queued rather than being pushed at the
// provider. Two reasons: the agent is a single conversation and a second
// concurrent turn is not a thing it can run, and a queued row is a promise Tallpine can
// keep across a restart, which a message dropped into a busy provider is not.
func (c *Controller) Send(ctx context.Context, msg ports.ChatUserMessage) (domain.ConversationTurn, error) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	return c.sendLocked(ctx, msg, true, nil, nil)
}

func (c *Controller) sendLocked(
	ctx context.Context,
	msg ports.ChatUserMessage,
	queueWhenBusy bool,
	providerOverride *ports.ChatUserMessage,
	followers []string,
) (domain.ConversationTurn, error) {
	atDispatch := false
	turn, err := c.sendLockedSteps(ctx, msg, queueWhenBusy, providerOverride, followers, &atDispatch)
	if err != nil && !atDispatch && len(followers) > 0 {
		// Failed before dispatch (handoff, continuation, store write): the claimed
		// batch never reached the provider, so it goes back to the queue.
		if relErr := c.store.ReleaseQueuedBatch(context.WithoutCancel(ctx), c.conversation.ID, followers); relErr != nil {
			c.log.Error("failed to release queued batch", "error", relErr)
		}
	}
	return turn, err
}

func (c *Controller) sendLockedSteps(
	ctx context.Context,
	msg ports.ChatUserMessage,
	queueWhenBusy bool,
	providerOverride *ports.ChatUserMessage,
	followers []string,
	atDispatch *bool,
) (domain.ConversationTurn, error) {
	if msg.ClientPayloadHash == "" {
		var err error
		msg.ClientPayloadHash, err = clientPayloadHash(msg)
		if err != nil {
			return domain.ConversationTurn{}, err
		}
	}
	c.mu.Lock()
	handoff := c.handoff != controllerHandoffNone
	c.mu.Unlock()
	if handoff {
		return domain.ConversationTurn{}, ErrControllerHandoff
	}

	// Bind retries to the client request before adding durable continuation context.
	var err error
	msg, err = c.prepareContinuation(ctx, msg)
	if err != nil {
		return domain.ConversationTurn{}, err
	}

	now := c.now()
	turnID := c.newID()
	// Automated sends (nudges, worker messages) to a session whose provider quota
	// is exhausted wait in the queue until the reset instead of starting a turn
	// that fails at once. A human's own message still goes through, ahead of the
	// queued automation, which stays queued.
	_, paused := c.usageLimitPausedUntil(ctx)
	humanSend := msg.Origin == domain.MessageOriginHuman
	pausedAutomation := paused && !humanSend
	if pausedAutomation {
		c.requestFailover()
	}
	queuedAhead := false
	if queueWhenBusy && !c.busy() && (!paused || !humanSend) {
		// Worker sends and reports keep their own boundary: a user message still
		// goes ahead of them rather than waiting behind stale status.
		if head, err := c.store.NextQueuedTurn(ctx, c.conversation.ID); err == nil {
			queuedAhead = !domain.QueueBoundaryProtected(head.ClientMessageID)
		} else if !errors.Is(err, domain.ErrNoQueuedTurn) {
			return domain.ConversationTurn{}, err
		}
	}
	shouldQueue := queueWhenBusy && (c.busy() || queuedAhead || pausedAutomation)
	markedSending := false
	if !shouldQueue {
		c.setSendingTurn(turnID)
		markedSending = true
	}
	dispatched := false
	// The marker outlives a send that dispatched, but not one that did not. A failed
	// insert, a duplicate, a queued row or a dispatch error leaves no running turn,
	// and a stale marker would report a still-queued row as running.
	defer func() {
		if markedSending && !dispatched {
			c.clearSendingTurn(turnID)
		}
	}()
	deliveryContent := ""
	if len(msg.Content) > 0 {
		encoded, err := json.Marshal(msg.Content)
		if err != nil {
			return domain.ConversationTurn{}, fmt.Errorf("encode chat delivery content: %w", err)
		}
		deliveryContent = string(encoded)
	}
	record := domain.ConversationMessage{
		ID:                  c.newID(),
		Text:                msg.Text,
		Origin:              normalizeOrigin(msg.Origin),
		ClientMessageID:     msg.ClientMessageID,
		ClientPayloadHash:   msg.ClientPayloadHash,
		DeliveryContentJSON: deliveryContent,
		SenderSessionID:     msg.SenderSessionID,
		SenderProjectID:     msg.SenderProjectID,
		SenderDisplayName:   msg.SenderDisplayName,
		AuthoredByUser:      msg.AuthoredByUser,
		Continuation:        msg.Continuation,
		InteractionAt:       msg.InteractionAt,
	}

	var created bool
	if c.reviewID == "" {
		created, err = c.store.AppendUserMessage(ctx, c.conversation.ID, c.sessionID, c.generation, record, turnID, now)
	} else {
		created, err = c.store.AppendReviewUserMessage(ctx, c.conversation.ID, c.sessionID, c.reviewID, c.generation, record, turnID, now)
	}
	if err != nil {
		return domain.ConversationTurn{}, fmt.Errorf("record user message: %w", err)
	}
	if !created {
		// Already delivered under this client message id. Returning the empty turn
		// signals "nothing new happened" without claiming a second dispatch.
		c.log.Debug("duplicate chat send ignored",
			"session", c.sessionID, "clientMessageId", msg.ClientMessageID)
		return domain.ConversationTurn{}, nil
	}

	if shouldQueue {
		// AppendUserMessage wrote it as queued, which is exactly where it belongs
		// until the running turn ends. drain picks it up from there.
		c.mu.Lock()
		c.log.Info("chat send queued behind busy controller",
			"session", c.sessionID, "clientMessageId", msg.ClientMessageID, "turn", turnID,
			"pendingTurn", c.pendingTurnID, "compactionPending", c.compactionPending, "state", c.state)
		c.mu.Unlock()
		queuedTurn := domain.ConversationTurn{
			ID:                 turnID,
			ConversationID:     c.conversation.ID,
			HandledBySessionID: c.sessionID,
			HandledByReviewID:  c.reviewID,
			State:              domain.TurnStateQueued,
			RequestedAt:        now,
		}
		if queuedAhead && !c.busy() {
			if err := c.drainLocked(ctx, true); err != nil {
				return queuedTurn, err
			}
		}
		return queuedTurn, nil
	}

	providerMessage := msg
	if providerOverride != nil {
		providerMessage = *providerOverride
	}
	*atDispatch = true
	turn, err := c.dispatch(ctx, turnID, providerMessage, now, false, followers)
	dispatched = err == nil
	return turn, err
}

// RetryTurn re-dispatches a failed turn's durable prompt as a new turn.
//
// The content is loaded from Tallpine's own rows, never from the caller, so the daemon
// owns what gets sent again. The original failed turn is never mutated or
// relabeled: the retry is a brand-new turn and both attempts stay in history.
//
// Idempotency: each retry turn stores an explicit, unique link to its source.
// A replayed request — an uncertain network round trip, a double-click, a
// client retry — looks that relation up before anything else and returns the
// turn that already exists, so caller-controlled message ids cannot counterfeit
// or consume a retry. A deliberate further attempt retries the failed child:
// the chain A -> B -> C is built from distinct sources, never by re-sending A.
// The current next-turn settings apply.
func (c *Controller) RetryTurn(ctx context.Context, turnID string) (domain.ConversationTurn, error) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	return c.retryTurnLocked(ctx, turnID, false, false)
}

// retryFailoverTurn is the retry after a usage-limit account move: manual
// consent semantics, but a worker or orchestrator's automation-origin prompt is
// replayed like the armed path replays it.
func (c *Controller) retryFailoverTurn(ctx context.Context, turnID string) (domain.ConversationTurn, error) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	return c.retryTurnLocked(ctx, turnID, false, true)
}

func (c *Controller) retryTurnLocked(ctx context.Context, turnID string, autoResume, replayAutomation bool) (domain.ConversationTurn, error) {
	if c.handoffActive() {
		return domain.ConversationTurn{}, ErrControllerHandoff
	}

	source, err := c.store.TurnByID(ctx, turnID)
	if err != nil {
		return domain.ConversationTurn{}, err
	}
	if source.ConversationID != c.conversation.ID {
		return domain.ConversationTurn{}, fmt.Errorf("%w: %s", domain.ErrNoConversationTurn, turnID)
	}
	if source.State != domain.TurnStateFailed || source.RolledBackAt != nil {
		return domain.ConversationTurn{}, fmt.Errorf("%w: %s", ErrTurnNotRetryable, turnID)
	}

	prompt, err := c.store.RetryPrompt(ctx, c.conversation.ID, turnID)
	if errors.Is(err, domain.ErrNoConversationTurn) {
		return domain.ConversationTurn{}, fmt.Errorf("%w: %s", ErrTurnNotRetryable, turnID)
	}
	if err != nil {
		return domain.ConversationTurn{}, fmt.Errorf("read retry prompt %s: %w", turnID, err)
	}
	if !prompt.ActiveLineage {
		return domain.ConversationTurn{}, fmt.Errorf("%w: %s", ErrRetryStaleBranch, turnID)
	}
	if prompt.Origin != domain.MessageOriginHuman && (!replayAutomation || !usageLimitReplayable(prompt.Origin, c.kind)) {
		return domain.ConversationTurn{}, fmt.Errorf("%w: %s", ErrTurnNotRetryable, turnID)
	}

	// A dispatch the provider never acknowledged may still have been accepted:
	// settling it failed was Tallpine's safe guess, not a fact about delivery.
	// Re-dispatching such a prompt could run the work twice, which is the one
	// outcome a retry must never have. Refuse until the caller confirms.
	if source.ProviderTurnID == "" {
		return domain.ConversationTurn{}, fmt.Errorf("%w: %s", ErrRetryDeliveryUncertain, turnID)
	}

	// The replay check comes before the busy check on purpose: returning an
	// already-recorded attempt must stay true even while that attempt runs.
	if existingID, found, lookupErr := c.store.RetryTurnIDForSource(ctx, c.conversation.ID, turnID); lookupErr != nil {
		return domain.ConversationTurn{}, lookupErr
	} else if found {
		existing, existingErr := c.store.TurnByID(ctx, existingID)
		if existingErr != nil {
			return domain.ConversationTurn{}, existingErr
		}
		return existing, nil
	}

	if c.busy() {
		return domain.ConversationTurn{}, ErrTurnRunning
	}

	if prompt.ProviderInputText != "" {
		prompt.Text = prompt.ProviderInputText
	}
	content, err := retryPromptContent(prompt.DeliveryContentJSON, c.Capabilities())
	if err != nil {
		return domain.ConversationTurn{}, err
	}

	now := c.now()
	newTurnID := c.newID()
	key := retryClientMessagePrefix + newTurnID
	retryMessage := domain.ConversationMessage{
		ID:                  c.newID(),
		Text:                prompt.Text,
		Origin:              prompt.Origin,
		ClientMessageID:     key,
		DeliveryContentJSON: prompt.DeliveryContentJSON,
		Continuation:        prompt.Continuation,
	}
	var created bool
	if autoResume {
		store, ok := c.store.(usageLimitStore)
		if !ok {
			return domain.ConversationTurn{}, ErrUsageLimitAutoResumeInvalid
		}
		created, err = store.AppendAutoResumeRetryUserMessage(ctx, c.conversation.ID, c.sessionID, c.generation, retryMessage, newTurnID, turnID, now)
	} else if c.reviewID == "" {
		created, err = c.store.AppendRetryUserMessage(ctx, c.conversation.ID, c.sessionID, c.generation, retryMessage, newTurnID, turnID, now)
	} else {
		created, err = c.store.AppendReviewRetryUserMessage(ctx, c.conversation.ID, c.sessionID, c.reviewID, c.generation, retryMessage, newTurnID, turnID, now)
	}
	if err != nil {
		return domain.ConversationTurn{}, fmt.Errorf("record retried message: %w", err)
	}
	if !created {
		// Lost a race with a concurrent identical click. Report the turn it
		// created rather than opening a second one.
		if existingID, found, lookupErr := c.store.RetryTurnIDForSource(ctx, c.conversation.ID, turnID); lookupErr == nil && found {
			if existing, existingErr := c.store.TurnByID(ctx, existingID); existingErr == nil {
				return existing, nil
			}
		}
		return domain.ConversationTurn{}, ErrTurnNotRetryable
	}

	return c.dispatch(ctx, newTurnID, ports.ChatUserMessage{
		Text:            prompt.Text,
		Continuation:    prompt.Continuation,
		Content:         content,
		Origin:          prompt.Origin,
		ClientMessageID: key,
	}, now, false, nil)
}

// retryPromptContent reconstructs provider-neutral durable prompt blocks and
// refuses anything that would be corrupted or silently discarded by the
// currently attached provider.
func retryPromptContent(raw string, capabilities ports.ChatCapabilities) ([]ports.ChatContent, error) {
	if raw == "" {
		return nil, nil
	}
	var content []ports.ChatContent
	if err := json.Unmarshal([]byte(raw), &content); err != nil {
		return nil, fmt.Errorf("%w: attachments are not valid JSON", ErrRetryContentInvalid)
	}
	if len(content) == 0 {
		return nil, fmt.Errorf("%w: attachment data contains no content blocks", ErrRetryContentInvalid)
	}
	for _, item := range content {
		switch item.Type {
		case "text":
			if !item.Internal || strings.TrimSpace(item.Text) == "" {
				return nil, fmt.Errorf("%w: text context must be Tallpine-owned and nonempty", ErrRetryContentInvalid)
			}
		case "image":
			if item.Data == "" || !strings.HasPrefix(strings.ToLower(item.MIMEType), "image/") {
				return nil, fmt.Errorf("%w: image attachments require data and an image MIME type", ErrRetryContentInvalid)
			}
			if !capabilities.Has(ports.ChatCapabilityImages) {
				return nil, fmt.Errorf("%w: image attachments are unsupported", ErrRetryUnsupported)
			}
		case "excerpt":
			if item.Excerpt == nil {
				return nil, fmt.Errorf("%w: chat excerpts require verified context", ErrRetryContentInvalid)
			}
		case "resource":
			if item.URI == "" {
				return nil, fmt.Errorf("%w: embedded resources require a URI", ErrRetryContentInvalid)
			}
			if !capabilities.Has(ports.ChatCapabilityEmbeddedContext) {
				return nil, fmt.Errorf("%w: embedded resources are unsupported", ErrRetryUnsupported)
			}
		case "resource_link":
			if item.URI == "" || item.Name == "" {
				return nil, fmt.Errorf("%w: resource links require a URI and name", ErrRetryContentInvalid)
			}
			if !capabilities.Has(ports.ChatCapabilityResourceLinks) {
				return nil, fmt.Errorf("%w: resource links are unsupported", ErrRetryUnsupported)
			}
		default:
			return nil, fmt.Errorf("%w: unsupported attachment type %q", ErrRetryContentInvalid, item.Type)
		}
	}
	return content, nil
}
