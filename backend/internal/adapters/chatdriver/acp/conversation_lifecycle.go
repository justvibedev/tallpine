package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/persistenthost"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func (c *conversation) Interrupt(ctx context.Context, providerTurnID string) error {
	c.mu.Lock()
	active := c.activeTurn
	sessionID := c.sessionID
	if active == "" || c.settlingTurn == active || (providerTurnID != "" && providerTurnID != active) {
		c.mu.Unlock()
		return ports.ErrChatNoActiveTurn
	}
	if c.interrupt != nil {
		c.mu.Unlock()
		return fmt.Errorf("ACP session/cancel is already in progress")
	}
	attempt := &interruptAttempt{turnID: active, done: make(chan struct{})}
	c.interrupt = attempt
	c.mu.Unlock()

	err := c.conn.Cancel(ctx, acpsdk.CancelNotification{SessionId: acpsdk.SessionId(sessionID)})
	c.mu.Lock()
	attempt.err = err
	close(attempt.done)
	if err != nil && c.interrupt == attempt {
		// The notification never crossed the connection, so keep the local
		// Prompt alive and allow a later retry to make a fresh attempt.
		c.interrupt = nil
	}
	c.mu.Unlock()
	if err != nil {
		return fmt.Errorf("ACP session/cancel: %w", err)
	}
	// The provider owns the root Prompt until it returns a terminal result. Keep
	// Tallpine busy after accepting session/cancel so a restart cannot start a second
	// root prompt while the first one is still executing downstream.
	return nil
}

func (c *conversation) ResolveRequest(
	ctx context.Context,
	requestID string,
	decision ports.ChatDecision,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	request, ok := c.pending[requestID]
	if !ok {
		c.mu.Unlock()
		return ports.ErrChatRequestNotPending
	}
	offeredRaw, offered := request.options[decision.ID]
	if !offered {
		c.mu.Unlock()
		return ports.ErrChatDecisionNotOffered
	}
	if len(decision.Raw) > 0 {
		if !bytes.Equal(bytes.TrimSpace(decision.Raw), bytes.TrimSpace(offeredRaw)) {
			c.mu.Unlock()
			return ports.ErrChatDecisionNotOffered
		}
	}
	delete(c.pending, requestID)
	c.mu.Unlock()
	command := persistentInteractionCommand{
		RequestID: requestID, Kind: persistentInteractionApproval,
		Decision: &persistentDecision{ID: decision.ID},
	}
	eventID, err := c.recordPersistentInteraction(ctx, command)
	if err != nil {
		c.mu.Lock()
		if !c.closed {
			c.pending[requestID] = request
		}
		c.mu.Unlock()
		return err
	}
	command.EventID = eventID

	c.emit(persistentInteractionEvent(command))
	request.result <- decision.ID
	return nil
}

// discard rolls back setup without leaving a newly created host behind.
// A failed adoption has no authority to terminate an existing provider.
func (c *conversation) discard() {
	if c.unadoptedForkID != "" && c.canDeleteFork {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err := c.conn.UnstableDeleteSession(ctx, acpsdk.UnstableDeleteSessionRequest{SessionId: acpsdk.SessionId(c.unadoptedForkID)})
		if err != nil {
			c.log.Warn("failed to delete unadopted ACP fork", "error", err)
		}
		cancel()
		c.unadoptedForkID = ""
	}
	if c.proc.reconnected {
		_ = c.Close()
	} else {
		_ = c.Terminate()
	}
}

func (c *conversation) Close() error {
	return c.closeProvider(false, false)
}

// Terminate destroys the provider host. Close deliberately only detaches during
// daemon shutdown or updater replacement.
func (c *conversation) Terminate() error {
	return c.closeProvider(true, true)
}

func (c *conversation) CanHibernate(ctx context.Context) (bool, error) {
	if c.hibernationCheck == nil {
		return true, nil
	}
	c.mu.Lock()
	id := c.sessionID
	c.mu.Unlock()
	return c.hibernationCheck(ctx, c.conn, acpsdk.SessionId(id))
}

// Hibernate releases the bridge and its provider without closing the native
// ACP session. session/close can delete the resume state on some agents.
func (c *conversation) Hibernate() error {
	return c.closeProvider(true, false)
}

func (c *conversation) closeProvider(terminate, closeSession bool) error {
	var closeErr error
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		close(c.closing)
		persistent := c.proc.terminate != nil
		c.detaching = persistent && !terminate
		cancel := c.turnCancel
		sessionID := c.sessionID
		c.mu.Unlock()
		if persistent && !terminate {
			closeErr = c.proc.stop()
			return
		}
		if cancel != nil {
			cancel()
		}
		c.failPendingPermissions()
		c.failPendingInputs()
		if closeSession && sessionID != "" {
			// The provider may save its session while answering; an answered close
			// lets the host stop it sooner.
			wait := sessionCloseWait
			if c.proc != nil && c.proc.quickStop.Load() {
				wait = quickSessionCloseWait
			}
			closeCtx, cancelClose := context.WithTimeout(context.Background(), wait)
			if _, err := c.conn.CloseSession(closeCtx, acpsdk.CloseSessionRequest{SessionId: acpsdk.SessionId(sessionID)}); err == nil && c.proc != nil {
				c.proc.quickStop.Store(true)
			}
			cancelClose()
		}
		if persistent {
			closeErr = c.proc.terminate()
		} else {
			closeErr = c.proc.stop()
		}
	})
	return closeErr
}

// AcknowledgeProviderEvent lets the host discard a prompt journal only after
// the terminal event was committed by the controller.
func (c *conversation) AcknowledgeProviderEvent(ctx context.Context, providerEventID string) error {
	c.mu.Lock()
	terminal := c.terminalEventID
	c.mu.Unlock()
	if providerEventID == "" || providerEventID != terminal || c.proc.terminate == nil {
		return nil
	}
	return c.conn.NotifyExtension(ctx, persistenthost.ACPPromptAckMethod, map[string]string{
		"eventId": providerEventID,
	})
}

func (c *conversation) watchConnection() {
	<-c.conn.Done()
	c.mu.Lock()
	detaching := c.detaching
	preserve := !c.closed && c.proc.terminate != nil
	c.mu.Unlock()
	// SDK Done cancels only its logical connection. Release both the forwarding
	// pipe and the actual socket, including a host blocked writing replay under
	// its ownership lock. Stopping this transport must not terminate the host.
	_ = c.legacyWire.reader.Close()
	_ = c.proc.stop()
	if !detaching && !preserve {
		c.failPendingPermissions()
		c.failPendingInputs()
	}
	event := ports.ChatEvent{Kind: ports.ChatEventControllerState, ControllerState: ports.ChatControllerStopped}
	if preserve {
		event.Err = ports.ErrChatRecoveryInconclusive
	}
	c.eventMu.Lock()
	if !c.eventsClosed {
		// Deliver the recovery boundary even though conn.Done is already closed;
		// emit's disconnect select could otherwise drop this ownership signal.
		c.eventsClosed = true
		c.finishEvents(event)
	}
	c.eventMu.Unlock()
}

func (c *conversation) emit(event ports.ChatEvent) {
	if c.captureHistoryEvent(event) {
		return
	}
	c.eventMu.RLock()
	defer c.eventMu.RUnlock()
	if c.eventsClosed {
		return
	}
	journaled := c.proc != nil && c.proc.terminate != nil
	for {
		queued, room := c.offerEvent(event)
		if !queued && !journaled {
			// An unjournaled stream has no replay to recover from, so nothing it
			// sends may be dropped. Past eventBuffer a delta merges into the queued
			// delta for the same item and anything else grows the backlog, all
			// without blocking the reader that the provider's responses share.
			queued, room = c.spillEvent(event)
		}
		if queued {
			return
		}
		// Full: a journaled conversation at eventBuffer, an unjournaled one only
		// past eventSpillBytes of queued events. Wait for room so the
		// provider's pipe carries the backpressure. Host-journaled frames must
		// reach durable projection; dropping one here would make a successful
		// replay look exactly-once while losing content.
		//
		// This goroutine also reads provider responses, so waiting here would
		// wedge a projector that waited on one. The only provider calls the
		// projector makes synchronously are the queue drain's after a turn or
		// compaction completes, and by then it has consumed everything emitted
		// before that completion: the queue holds only what the provider sent
		// since, far below either bound.
		var disconnected <-chan struct{}
		if c.conn != nil {
			disconnected = c.conn.Done()
		}
		select {
		case <-room:
		case <-disconnected:
			return
		case <-c.closing:
			return
		}
	}
}

func (c *conversation) settleOpenItems(turnID string, turnState ...domain.TurnState) {
	c.mu.Lock()
	messages := c.messages
	thoughts := c.thoughts
	nestedMessages := c.nestedMessages
	tools := c.tools
	c.mu.Unlock()
	recovered := len(turnState) > 0 && turnState[0] == domain.TurnStateRecovered
	activityStatus := domain.ActivityStatusCompleted
	if recovered {
		activityStatus = domain.ActivityStatusRecovered
	}
	messageIDs := sortedKeys(messages)
	for _, id := range messageIDs {
		text := messages[id]
		c.emit(ports.ChatEvent{Kind: ports.ChatEventMessageCompleted, ProviderTurnID: turnID, ProviderItemID: id, Text: text})
	}
	thoughtIDs := sortedKeys(thoughts)
	for _, id := range thoughtIDs {
		text := thoughts[id]
		c.emit(ports.ChatEvent{Kind: ports.ChatEventActivityCompleted, ProviderTurnID: turnID,
			ProviderItemID: id, ActivityKind: domain.ActivityKindReasoning,
			ActivityStatus: activityStatus, Summary: "Reasoning", Text: text})
	}
	nestedIDs := sortedKeys(nestedMessages)
	for _, id := range nestedIDs {
		item := nestedMessages[id]
		detail, _ := json.Marshal(map[string]any{"parentProviderItemId": item.parentID, "nestedAgent": true})
		c.emit(ports.ChatEvent{Kind: ports.ChatEventActivityCompleted, ProviderTurnID: turnID,
			ProviderItemID: id, ActivityKind: domain.ActivityKindMCPTool,
			ActivityStatus: activityStatus, Summary: "Subagent response",
			Text: item.text, Detail: detail})
	}
	toolIDs := sortedKeys(tools)
	for _, id := range toolIDs {
		tool := tools[id]
		if tool.status == acpsdk.ToolCallStatusPending || tool.status == acpsdk.ToolCallStatusInProgress || tool.status == "" {
			snapshot := *tool
			snapshot.status = acpsdk.ToolCallStatusFailed
			event := c.toolEvent(turnID, &snapshot, true)
			if recovered {
				event.ActivityStatus = domain.ActivityStatusRecovered
			}
			c.emit(event)
			// A cancelled compaction gets no terminal report from the adapter.
			c.emitCompaction(&snapshot, turnID, c.emit)
		}
	}
}

func sortedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (c *conversation) failPendingPermissions() {
	c.mu.Lock()
	pending := c.pending
	c.pending = make(map[string]*parkedPermission)
	c.mu.Unlock()
	for _, request := range pending {
		select {
		case request.result <- "":
		default:
		}
	}
}

func (c *conversation) failPendingInputs() {
	c.mu.Lock()
	pending := c.pendingInputs
	c.pendingInputs = make(map[string]*parkedInput)
	c.mu.Unlock()
	for _, request := range pending {
		select {
		case request.result <- ports.ChatInputResponse{Action: ports.ChatInputActionCancel}:
		default:
		}
	}
}
