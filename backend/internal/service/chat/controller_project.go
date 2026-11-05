package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// project consumes the driver's normalized events and writes them down. It runs
// until this controller's stream closes. That can mean provider termination or a
// deliberate detach from a provider that remains alive in a persistent host.
func (c *Controller) project(flushInterval time.Duration) {
	defer close(c.stopped)

	// Detached from any request context: this outlives the call that started the
	// controller, and must keep persisting until the provider stream ends.
	ctx := context.WithoutCancel(context.Background())
	acknowledger, persistent := c.conv.(ports.ChatProviderEventAcknowledger)

	if !pumpProviderEventsWith(c.conv.Events(), c.flushRequests, c.deltaPolicy(flushInterval), func(unit []ports.ChatEvent) bool {
		return c.projectUnit(ctx, unit, acknowledger, persistent)
	}) {
		return
	}

	c.mu.Lock()
	c.state = ports.ChatControllerStopped
	suppressStoppedActivity := c.suppressStoppedActivity
	preserveProvider := c.preserveProviderOnStop
	hibernating := c.handoff == controllerHandoffHibernate
	c.mu.Unlock()
	// Hibernate admitted no running work, but Send can append an optimistic
	// message during shutdown. Keep that queue for the replacement controller.
	if preserveProvider || hibernating {
		return
	}

	// The stream has ended, so nothing more can arrive for this controller. This
	// is the only place that reliably knows that — a provider process can die on
	// its own, in which case no Tallpine code path called Close — so it is where
	// in-flight work gets settled.
	//
	// A turn the controller was running is not evidence the work finished, and an
	// approval still pending can never be answered because the provider call it
	// was blocking is gone. Both are closed out honestly rather than left looking
	// live forever.
	now := c.now()
	// The provider stream ending is the one universal stop signal. Some drivers
	// emit an explicit controller-state event first, but a process crash or lost
	// transport cannot. Report the same lifecycle boundary here so the session
	// does not remain durably active, idle, or blocked after its controller died.
	// ControllerGeneration fences this write from a replacement controller.
	if !suppressStoppedActivity {
		c.reportActivity(ctx, domain.ActivityExited, "chat.controller.stopped", now)
	}
	if err := c.cleanupOwnedControllerWork(ctx, now); err != nil {
		c.log.Error("failed to clean up stopped controller work", "session", c.sessionID, "error", err)
	}
}

// projectUnit commits one unit from the event pump: a single event, or a run of
// deltas for one provider item. It reports false when consumption must stop
// because a persistent provider's projection failed.
func (c *Controller) projectUnit(
	ctx context.Context,
	unit []ports.ChatEvent,
	acknowledger ports.ChatProviderEventAcknowledger,
	persistent bool,
) bool {
	if c.coalescing() {
		streamed := false
		for i := range unit {
			streamed = streamed || unit[i].Kind == ports.ChatEventCommandOutputDelta
			unit[i] = c.coalesceSnapshot(unit[i], streamed)
		}
	}
	event := unit[0]
	if event.Kind == ports.ChatEventControllerState && event.ControllerState == ports.ChatControllerStopped &&
		errors.Is(event.Err, ports.ErrChatRecoveryInconclusive) {
		// A lost attachment is not proof the persistent provider stopped. Keep
		// its turn/request correlation and queued intake for a replacement.
		c.mu.Lock()
		preserver, persistentProvider := c.conv.(ports.ChatProviderPreserver)
		c.transportInterrupted = !c.explicitClose && persistentProvider && preserver.PreservesProviderOnClose()
		c.preserveProviderOnStop = true
		c.state = ports.ChatControllerStopped
		suppressStoppedActivity := c.suppressStoppedActivity
		c.mu.Unlock()
		if !suppressStoppedActivity {
			c.reportActivity(ctx, domain.ActivityExited, "chat.controller.disconnected", c.now())
		}
		return true
	}
	c.mu.Lock()
	preserveWork := c.preserveProviderOnStop || c.handoff == controllerHandoffHibernate
	c.mu.Unlock()
	if preserveWork && event.Kind == ports.ChatEventControllerState && event.ControllerState == ports.ChatControllerStopped {
		return true
	}
	// A lifecycle event and a concurrent Send must agree on whether the root
	// conversation is busy. Holding the same lock Send/dispatch use closes the
	// window between the durable projection and the in-memory ownership update.
	lifecycle := event.Kind == ports.ChatEventTurnStarted || event.Kind == ports.ChatEventTurnCompleted || event.Kind == ports.ChatEventCompacted
	if event.Kind == ports.ChatEventTurnStarted {
		c.noteProviderTurnStarted(event)
	}
	if lifecycle {
		c.sendMu.Lock()
	}
	project := func() (bool, bool, error) {
		if coalescible(event) {
			projected, err := c.projectDeltas(ctx, unit)
			return projected, false, err
		}
		return c.projectEvent(ctx, event, false)
	}
	projected, primaryTurn, err := project()
	// A terminal receipt compacts the entire prompt, so it cannot pass a
	// failed earlier projection. Retry transient store errors in order; if
	// still failing, detach and leave the journal for a replacement controller.
	for attempt := 0; err != nil && persistent && attempt < 3; attempt++ {
		time.Sleep(20 * time.Millisecond)
		projected, primaryTurn, err = project()
	}
	for _, committed := range unit {
		if err == nil && persistent && committed.ProviderEventID != "" {
			err = acknowledger.AcknowledgeProviderEvent(ctx, committed.ProviderEventID)
		}
	}
	if err != nil && persistent {
		c.log.Error("persistent chat projection stopped; provider retained for replay",
			"session", c.sessionID, "kind", event.Kind, "error", err)
		c.mu.Lock()
		c.preserveProviderOnStop = true
		c.state = ports.ChatControllerStopped
		c.mu.Unlock()
		if lifecycle {
			c.sendMu.Unlock()
		}
		c.once.Do(func() { c.closeErr = c.conv.Close(); c.asyncTasksGone() })
		return false
	}
	if err == nil {
		for _, committed := range unit {
			c.coalesce.observe(committed)
			c.observeAssistantText(committed)
		}
	}
	if err != nil {
		// A projection failure must not kill the provider stream. The store
		// rolls the archive back with its projection, so durable state remains
		// internally consistent and a later provider replay may retry it.
		c.log.Error("failed to project chat event",
			"session", c.sessionID, "kind", event.Kind, "error", err)
	} else if projected {
		c.afterProject(ctx, event, primaryTurn)
	}
	if lifecycle {
		c.sendMu.Unlock()
	}
	return true
}

func (c *Controller) cleanupOwnedControllerWork(ctx context.Context, now time.Time) error {
	if c.reviewID != "" {
		_, err := c.store.CleanupOwnedReviewControllerWork(ctx, c.reviewID, c.conversation.ID, c.generation, now)
		return err
	}
	_, err := c.store.CleanupOwnedControllerWork(ctx, c.sessionID, c.conversation.ID, c.generation, now)
	return err
}

// projectEvent archives one normalized provider event and applies its durable
// projection in the same SQLite transaction.
func (c *Controller) projectEvent(ctx context.Context, event ports.ChatEvent, historical bool) (bool, bool, error) {
	projected, err := c.archive(ctx, event, func(txCtx context.Context) error { return c.apply(txCtx, event, historical) })
	if err != nil || !projected {
		return projected, false, err
	}
	c.publishCommittedCapacity(event)
	c.publishCommittedAsyncTask(event)
	return true, c.applyCommittedTurnLifecycle(event), nil
}

// publishCommittedCapacity hands a Codex capacity observation to the daemon's
// account state only after its archive row commits. Publishing from inside the
// projection transaction announced observations a rollback then discarded, and
// let a reader act on the update before the archive held it.
func (c *Controller) publishCommittedCapacity(event ports.ChatEvent) {
	if event.Kind != ports.ChatEventRateLimits || c.harness != domain.HarnessCodex ||
		event.RateLimits == nil || event.RateLimits.CodexCapacity == nil || c.onCodexCapacityChanged == nil {
		return
	}
	c.onCodexCapacityChanged(c.sessionID, c.generation, *event.RateLimits.CodexCapacity)
}

// publishCommittedAsyncTask hands a background-task report to the daemon only
// after its archive row commits. The daemon's tracker announces the change with
// a store write, which takes the store's single writer lock: calling it from
// inside the projection transaction, which holds that lock, deadlocked every
// store write the first time a session started a background task.
func (c *Controller) publishCommittedAsyncTask(event ports.ChatEvent) {
	if event.Kind != ports.ChatEventAsyncTask || event.AsyncTask == nil || c.onAsyncTask == nil || c.reviewID != "" {
		return
	}
	c.onAsyncTask(c.sessionID, *event.AsyncTask)
}

// archive records event in the provider archive and runs project in the same
// transaction. It reports false without projecting when the controller
// generation is stale or the event's native identity is already archived.
func (c *Controller) archive(ctx context.Context, event ports.ChatEvent, project func(context.Context) error) (bool, error) {
	record := map[string]any{
		"kind":                   event.Kind,
		"providerEventId":        event.ProviderEventID,
		"providerTurnId":         event.ProviderTurnID,
		"providerConversationId": event.ProviderConversationID,
		"providerItemId":         event.ProviderItemID,
		"providerItemAliases":    event.ProviderItemAliases,
		"clientMessageId":        event.ClientMessageID,
		"turnState":              event.TurnState,
		"delta":                  event.Delta,
		"text":                   event.Text,
		"activityKind":           event.ActivityKind,
		"activityStatus":         event.ActivityStatus,
		"summary":                event.Summary,
		"detail":                 json.RawMessage(nonEmptyJSON(event.Detail)),
		"requestId":              event.RequestID,
		"decisions":              event.Decisions,
		"controllerState":        event.ControllerState,
		"usage":                  event.Usage,
		"rateLimits":             event.RateLimits,
		"title":                  event.Title,
		"plan":                   event.Plan,
		"reroute":                event.Reroute,
		"account":                event.Account,
		"threadState":            event.ThreadState,
		"mcpServers":             event.MCPServers,
	}
	if c.harness == domain.HarnessCodex {
		// Codex account identity and subscription capacity are daemon-memory
		// account state. Conversation provider archives must not become a second
		// persistence path for email, plan, percentages, reset times, or raw
		// account payloads.
		if event.Kind == ports.ChatEventAccountChanged {
			record["account"] = nil
		}
		if event.Kind == ports.ChatEventRateLimits {
			record["rateLimits"] = nil
		}
	}
	record["diff"] = event.Diff
	dropEmptyArchiveFields(record)
	if event.Input != nil {
		record["input"] = event.Input
	}
	if event.Err != nil {
		record["error"] = event.Err.Error()
	}
	var (
		payload     []byte
		fingerprint [32]byte
		err         error
	)
	snapshot := event.Kind == ports.ChatEventActivityStarted && event.ProviderItemID != "" && c.coalescing()
	if snapshot {
		payload, fingerprint, err = snapshotArchivePayload(event, record, &c.coalesce)
	} else {
		payload, err = json.Marshal(record)
	}
	if err != nil {
		return false, fmt.Errorf("encode provider event archive: %w", err)
	}
	var projected bool
	if c.reviewID != "" {
		projected, err = c.store.ProjectReviewProviderEvent(ctx, c.conversation.ID, c.sessionID,
			c.reviewID, c.generation, event.ProviderEventID, string(event.Kind), string(payload), c.now(), project)
	} else {
		projected, err = c.store.ProjectProviderEvent(ctx, c.conversation.ID, c.sessionID,
			c.generation, event.ProviderEventID, string(event.Kind), string(payload), c.now(), project)
	}
	if err == nil && projected && snapshot {
		// Remembered as soon as the row is written, even inside a transaction
		// that later rolls back: the cost is one later repeat stored as a marker
		// against a row that is missing, and the projection itself is unaffected.
		c.coalesce.archivedSnapshot(event.ProviderItemID, fingerprint)
	}
	return projected, err
}

// primaryTurnStartLocked is the shared acceptance rule for acknowledgement,
// durable start ownership and the post-commit busy gate. Caller holds c.mu.
func (c *Controller) primaryTurnStartLocked(event ports.ChatEvent) bool {
	return event.ProviderTurnID != "" &&
		(event.ProviderConversationID == "" || event.ProviderConversationID == c.conv.ProviderConversationID()) &&
		(c.pendingTurnID == "" || c.pendingTurnID == event.ProviderTurnID)
}

// noteProviderTurnStarted records the provider's acknowledgement as soon as its
// turn-started event arrives, before the projection queues for sendMu. Steer,
// queued-turn promotion and render and artifact recording wait for this
// acknowledgement while holding sendMu, so recording it only after that lock
// made each of them sit out the whole turnAckWait. It is not ownership:
// pendingTurnID still moves only once the durable projection commits.
func (c *Controller) noteProviderTurnStarted(event ports.ChatEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.primaryTurnStartLocked(event) {
		c.ackedTurnID = event.ProviderTurnID
	}
}

// applyCommittedTurnLifecycle updates the controller's volatile ownership only
// after the matching durable projection commits. Codex streams events for nested
// child threads over the root connection, so only events from the conversation Tallpine
// opened are allowed to claim or release the primary turn.
func (c *Controller) applyCommittedTurnLifecycle(event ports.ChatEvent) bool {
	if event.Kind != ports.ChatEventTurnStarted && event.Kind != ports.ChatEventTurnCompleted {
		return false
	}
	if event.ProviderConversationID != "" && event.ProviderConversationID != c.conv.ProviderConversationID() {
		return false
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	switch event.Kind {
	case ports.ChatEventTurnStarted:
		// Tallpine serializes root dispatch. A different turn id while one is pending is
		// auxiliary provider work, even for a protocol that cannot name its thread.
		if !c.primaryTurnStartLocked(event) {
			return false
		}
		c.pendingTurnID = event.ProviderTurnID
		c.compactionPending = false
		c.ackedTurnID = event.ProviderTurnID
		c.state = ports.ChatControllerBusy
		return true
	case ports.ChatEventTurnCompleted:
		if c.pendingTurnID != event.ProviderTurnID && (!c.compactionPending || c.pendingTurnID != "") {
			// A completion that names a different turn leaves the controller busy. If
			// the pending turn never completes, every later send queues behind it.
			c.log.Info("chat turn completion ignored: provider turn does not match pending turn",
				"session", c.sessionID, "completedTurn", event.ProviderTurnID,
				"pendingTurn", c.pendingTurnID, "compactionPending", c.compactionPending)
			return false
		}
		c.compactionPending = false
		c.pendingTurnID = ""
		c.dispatchingTurnID = ""
		if c.ackedTurnID == event.ProviderTurnID {
			c.ackedTurnID = ""
		}
		c.state = ports.ChatControllerReady
		return true
	default:
		return false
	}
}
