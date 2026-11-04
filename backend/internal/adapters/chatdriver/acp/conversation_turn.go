package acp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/google/uuid"

	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/persistenthost"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// ActivateLiveReconnect installs the durable turn correlation before releasing
// replayed ACP updates to the SDK. This prevents a replacement daemon from
// attributing output to an empty turn or starting a second root prompt.
func (c *conversation) ActivateLiveReconnect(ctx context.Context, providerTurnID string) error {
	if !c.proc.reconnected || c.liveState == nil || c.proc.gate == nil {
		return nil
	}
	durableBusy := strings.TrimSpace(providerTurnID) != ""
	switch {
	case c.liveState.ActivePrompt && !durableBusy:
		return fmt.Errorf("%w: ACP host has an active prompt but no durable running turn",
			ports.ErrChatRecoveryInconclusive)
	case durableBusy && !c.liveState.ActivePrompt && c.liveState.PendingResultEventID == "":
		return fmt.Errorf("%w: durable turn %q is running but the ACP host is idle",
			ports.ErrChatRecoveryInconclusive, providerTurnID)
	case c.liveState.PendingResultEventID != "" && !durableBusy:
		// The old controller committed the terminal event but died before its ACK.
		// The replay is already queued on this socket, so acknowledge it and ignore
		// that one private completion after opening the reader gate.
		c.mu.Lock()
		c.ignorePromptResult = true
		c.terminalEventID = c.liveState.PendingResultEventID
		c.mu.Unlock()
		if err := c.conn.NotifyExtension(ctx, persistenthost.ACPPromptAckMethod, map[string]string{
			"eventId": c.liveState.PendingResultEventID,
		}); err != nil {
			return fmt.Errorf("acknowledge committed persistent ACP result: %w", err)
		}
	}
	c.mu.Lock()
	if durableBusy {
		c.activeTurn = providerTurnID
		if c.liveState.ActiveCompaction {
			c.compactingTurnID = providerTurnID
			c.compactionBefore = c.contextTokens
		}
		c.messages = make(map[string]string)
		c.thoughts = make(map[string]string)
		c.nestedMessages = make(map[string]nestedMessageState)
		c.tools = make(map[string]*toolState)
	}
	c.mu.Unlock()
	c.proc.gate.Open()
	return nil
}

// SendTurn prepares the long-lived ACP prompt request. Tallpine's controller starts it
// through StartDeferredTurn only after the provider turn id is durable.
func (c *conversation) SendTurn(ctx context.Context, msg ports.ChatUserMessage) (ports.ChatTurnRef, error) {
	if err := ctx.Err(); err != nil {
		return ports.ChatTurnRef{}, err
	}
	prompt, err := c.promptContent(msg)
	if err != nil {
		return ports.ChatTurnRef{}, err
	}
	c.mu.Lock()
	busy := c.closed || c.prepared != nil || c.activeTurn != ""
	c.mu.Unlock()
	if busy {
		return ports.ChatTurnRef{}, errors.New("ACP conversation already has a turn in flight")
	}
	if err := c.applyTurnSettings(ctx, msg.Settings); err != nil {
		return ports.ChatTurnRef{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ports.ChatTurnRef{}, errConversationClosed
	}
	if c.prepared != nil || c.activeTurn != "" {
		return ports.ChatTurnRef{}, errors.New("ACP conversation already has a turn in flight")
	}
	id := uuid.NewString()
	c.prepared = &preparedTurn{id: id, clientMessageID: msg.ClientMessageID, prompt: prompt}
	return ports.ChatTurnRef{ProviderTurnID: id}, nil
}

func (c *conversation) DiscardDeferredTurn(providerTurnID string) {
	c.mu.Lock()
	if c.prepared != nil && c.prepared.id == providerTurnID {
		c.prepared = nil
	}
	c.mu.Unlock()
}

func (c *conversation) StartDeferredTurn(providerTurnID string) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errConversationClosed
	}
	if c.prepared == nil || c.prepared.id != providerTurnID {
		c.mu.Unlock()
		return errors.New("ACP prepared turn not found")
	}
	turn := *c.prepared
	c.prepared = nil
	// Standing context leads the first prompt so the provider reads it as
	// context rather than as part of what the user asked. It is consumed here,
	// not in SendTurn, because this is the first point at which a prompt is
	// guaranteed to reach the provider: settings validation, a busy conversation
	// and a discarded prepared turn all fail in SendTurn, and spending the
	// preamble on any of them would silently drop Tallpine's instructions from the
	// session's first real turn.
	if !c.preambleSpent && len(c.preamble) > 0 {
		turn.prompt = append(append([]acpsdk.ContentBlock(nil), c.preamble...), turn.prompt...)
		c.preambleSpent = true
	}
	c.activeTurn = turn.id
	c.settlingTurn = ""
	turnCtx, cancel := context.WithCancel(context.Background())
	c.turnCancel = cancel
	sessionID := c.sessionID
	c.messages = make(map[string]string)
	c.thoughts = make(map[string]string)
	c.nestedMessages = make(map[string]nestedMessageState)
	c.tools = make(map[string]*toolState)
	c.turnDiffs = nil
	c.turnDiffTurnID = ""
	c.providerFailure = nil
	c.mu.Unlock()

	go c.runTurn(turnCtx, sessionID, turn)
	return nil
}

func (c *conversation) runTurn(ctx context.Context, sessionID string, turn preparedTurn) {
	c.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: turn.id})
	c.emit(ports.ChatEvent{Kind: ports.ChatEventControllerState, ControllerState: ports.ChatControllerBusy})
	// ACP message ids are opaque idempotency/correlation keys. Preserve Tallpine's
	// durable client id when possible so an agent that echoes it from session/load
	// can be reconciled without provider-specific knowledge. Some agents (notably
	// claude-agent-acp) assign their own persisted user uuid; the service's history
	// reconciliation is the fallback for those conforming-but-different agents.
	messageID := strings.TrimSpace(turn.clientMessageID)
	if messageID == "" {
		messageID = uuid.NewString()
	}
	resp, err := c.conn.Prompt(ctx, acpsdk.PromptRequest{
		SessionId: acpsdk.SessionId(sessionID),
		MessageId: &messageID,
		Prompt:    turn.prompt,
	})

	c.finishPrompt(turn.id, resp, err)
}

func (c *conversation) finishPrompt(
	turnID string,
	resp acpsdk.PromptResponse,
	err error,
) {
	eventID, _ := resp.Meta[persistenthost.ACPEventIDMetaKey].(string)
	var requestErr *acpsdk.RequestError
	if eventID == "" && errors.As(err, &requestErr) {
		if data, ok := requestErr.Data.(map[string]any); ok {
			eventID, _ = data[persistenthost.ACPEventIDMetaKey].(string)
		}
	}
	// A transport error may win the race with watchConnection. The host still
	// owns this prompt; its journal, not an SDK disconnect, supplies the outcome.
	if err != nil && eventID == "" && c.providerDetaching() {
		return
	}
	c.mu.Lock()
	if c.detaching {
		c.mu.Unlock()
		return
	}
	c.settlingTurn = turnID
	interrupt := c.interrupt
	isCompaction := c.compactingTurnID != "" && c.compactingTurnID == turnID
	answered := len(c.messages) > 0 || len(c.tools) > 0
	c.mu.Unlock()
	c.settleOpenItems(turnID)
	interruptedLocally := false
	if interrupt != nil && interrupt.turnID == turnID {
		// ACP cancellation and Prompt completion can race. Wait for the sender's
		// definitive result before classifying the turn so a failed notification
		// cannot look interrupted and an accepted one cannot look failed.
		<-interrupt.done
		interruptedLocally = interrupt.err == nil
	}
	var state domain.TurnState
	var turnErr error
	if err != nil {
		if interruptedLocally || errors.Is(err, context.Canceled) || isACPRequestCancelled(err) {
			state = domain.TurnStateInterrupted
		} else {
			state = domain.TurnStateFailed
			turnErr = normalizeACPError("ACP session/prompt", err)
		}
	} else {
		state = turnState(resp.StopReason)
		if c.promptResponseFailure != nil && state != domain.TurnStateInterrupted && !interruptedLocally {
			if failure := c.promptResponseFailure(resp); failure != nil {
				state = domain.TurnStateFailed
				turnErr = failure
			}
		}
		if c.sessionPromptResponseFailure != nil && turnErr == nil && state != domain.TurnStateInterrupted && !interruptedLocally {
			c.mu.Lock()
			sessionID := c.sessionID
			c.mu.Unlock()
			if failure := c.sessionPromptResponseFailure(sessionID, resp, answered); failure != nil {
				state = domain.TurnStateFailed
				turnErr = failure
			}
		}
		models := quotaModelUsage(resp.Meta)
		if c.turnUsage != nil {
			models = append(models, c.turnUsage(resp.Meta, c.configOptionCurrent("model"))...)
		}
		if len(models) > 0 {
			c.emit(ports.ChatEvent{Kind: ports.ChatEventUsage, Usage: &ports.ChatUsage{ModelUsage: models}})
		}
		if respUsageHasTokens(resp.Usage) {
			cached := 0
			if resp.Usage.CachedReadTokens != nil {
				cached += *resp.Usage.CachedReadTokens
			}
			if resp.Usage.CachedWriteTokens != nil {
				cached += *resp.Usage.CachedWriteTokens
			}
			c.emit(ports.ChatEvent{Kind: ports.ChatEventUsage, Usage: &ports.ChatUsage{
				InputTokens: int64(resp.Usage.InputTokens), OutputTokens: int64(resp.Usage.OutputTokens),
				CachedTokens: int64(cached), TotalTokens: int64(resp.Usage.TotalTokens),
				TotalsKnown: true,
			}})
		}
	}
	// A failed or cancelled turn was still billed, so the provider's own
	// accounting is read for every outcome unless the response carried usage.
	if c.sessionUsage != nil && !respUsageHasTokens(resp.Usage) {
		c.mu.Lock()
		sessionID := c.sessionID
		c.mu.Unlock()
		c.emitSessionUsage(sessionID)
	}
	if errors.Is(turnErr, ports.ErrChatAuthRequired) {
		// The provider has just contradicted whatever the readiness cache holds.
		// Correct it for both request errors and structured prompt failures.
		c.mu.Lock()
		c.authRejected = true
		c.mu.Unlock()
		if c.onAuthRejected != nil {
			c.onAuthRejected()
		}
	} else if turnErr == nil && state == domain.TurnStateCompleted {
		// A successful turn after a refusal on this connection is recovery. Every
		// other success stays quiet: each account report costs the daemon an
		// account probe, and the controller already reconciles durable recovery
		// (including after a restart) from the turn completion itself.
		c.mu.Lock()
		recovered := c.authRejected
		c.authRejected = false
		c.mu.Unlock()
		if recovered {
			c.emit(ports.ChatEvent{Kind: ports.ChatEventAccountChanged, Account: &ports.ChatAccount{ReauthRecovered: true}})
		}
	}
	if isCompaction {
		if state == domain.TurnStateCompleted {
			c.settleCompaction(turnID)
		} else {
			c.mu.Lock()
			c.compactingTurnID = ""
			c.compactionBefore = 0
			c.compactionSummary = ""
			c.mu.Unlock()
		}
	}
	// Only a durable host receipt replaces the outstanding one. A host-local
	// rejection carries no event ID; erasing the receipt here would make the
	// controller's later ACK of that receipt a no-op and leave the host refusing
	// every prompt until restart.
	if eventID != "" {
		c.mu.Lock()
		c.terminalEventID = eventID
		c.mu.Unlock()
	}
	c.emit(ports.ChatEvent{
		Kind: ports.ChatEventTurnCompleted, ProviderEventID: eventID,
		ProviderTurnID: turnID, TurnState: state, Err: turnErr,
	})
	// Release the turn before announcing readiness, not after. A consumer that
	// reacts to ChatControllerReady by sending the next turn — Tallpine's own queue
	// drain does exactly that — would otherwise race this unlock and be refused
	// with "already has a turn in flight", depending purely on which goroutine
	// the scheduler picks. Ready has to mean ready.
	c.mu.Lock()
	if c.activeTurn == turnID {
		c.activeTurn = ""
		c.settlingTurn = ""
		c.turnCancel = nil
		c.providerFailure = nil
		if c.interrupt == interrupt {
			c.interrupt = nil
		}
	}
	c.mu.Unlock()
	c.emit(ports.ChatEvent{Kind: ports.ChatEventControllerState, ControllerState: ports.ChatControllerReady})
}

func turnState(reason acpsdk.StopReason) domain.TurnState {
	switch reason {
	case acpsdk.StopReasonCancelled:
		return domain.TurnStateInterrupted
	case acpsdk.StopReasonEndTurn:
		return domain.TurnStateCompleted
	default:
		return domain.TurnStateFailed
	}
}

func (c *conversation) promptContent(message ports.ChatUserMessage) ([]acpsdk.ContentBlock, error) {
	c.mu.Lock()
	caps := cloneCapabilities(c.capabilities)
	c.mu.Unlock()
	prompt := make([]acpsdk.ContentBlock, 0, 1+len(message.Content))
	if strings.TrimSpace(message.Text) != "" {
		prompt = append(prompt, acpsdk.TextBlock(message.Text))
	}
	for _, item := range message.Content {
		switch item.Type {
		case "image":
			if !caps.Has(ports.ChatCapabilityImages) {
				return nil, fmt.Errorf("ACP agent does not support image prompts")
			}
			if item.Data == "" || item.MIMEType == "" {
				return nil, errors.New("image content requires data and MIME type")
			}
			prompt = append(prompt, acpsdk.ImageBlock(item.Data, item.MIMEType))
		case "resource_link":
			if item.URI == "" || item.Name == "" {
				return nil, errors.New("resource link requires a URI and name")
			}
			prompt = append(prompt, acpsdk.ResourceLinkBlock(item.Name, item.URI))
		case "resource":
			if !caps.Has(ports.ChatCapabilityEmbeddedContext) {
				return nil, fmt.Errorf("ACP agent does not support embedded context")
			}
			if item.URI == "" {
				return nil, errors.New("embedded resource requires a URI")
			}
			mimeType := (*string)(nil)
			if item.MIMEType != "" {
				mimeType = pointer(item.MIMEType)
			}
			resource := &acpsdk.TextResourceContents{
				Uri: item.URI, Text: item.Text, MimeType: mimeType,
			}
			if item.Internal {
				resource.Meta = map[string]any{tallpineInternalReplayMetaKey: true}
			}
			prompt = append(prompt, acpsdk.ResourceBlock(acpsdk.EmbeddedResourceResource{
				TextResourceContents: resource,
			}))
		default:
			return nil, fmt.Errorf("unsupported chat content type %q", item.Type)
		}
	}
	if len(prompt) == 0 {
		return nil, errors.New("chat message has no content")
	}
	return prompt, nil
}
