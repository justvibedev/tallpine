package chat

import (
	"context"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func (m *sideManager) attach(side domain.SideConversation, conv ports.ChatConversation) bool {
	ctx, cancel := context.WithCancel(m.ctx)
	runtime := &sideRuntime{side: side, conv: conv, cancel: cancel, done: ctx.Done(), ctx: ctx,
		completed: make(chan ports.ChatEvent, 8), messageText: make(map[string]string),
		messageIDs: make(map[string]string), activityIDs: make(map[string]string), activityText: make(map[string]string)}
	m.mu.Lock()
	if m.runID != side.AppRunID || m.ctx.Err() != nil {
		m.mu.Unlock()
		cancel()
		_ = conv.Close()
		return false
	}
	if err := m.store.SetSideReady(ctx, side.ID, side.Generation, side.ProviderForkID, m.service.now()); err != nil {
		m.mu.Unlock()
		cancel()
		_ = conv.Close()
		return false
	}
	turns, _, _ := m.store.SideTurns(ctx, side.ID, time.Time{}, 0)
	var active *domain.SideTurn
	for i := range turns {
		if turns[i].State == "running" && turns[i].ProviderTurnID != "" {
			active = &turns[i]
			break
		}
	}
	if active != nil {
		runtime.activeTurnID, runtime.providerTurnID = active.ID, active.ProviderTurnID
		messages, _ := m.store.SideMessages(ctx, side.ID, []string{active.ID})
		for _, message := range messages {
			if message.ProviderItemID != "" {
				runtime.messageIDs[message.ProviderItemID] = message.ID
				runtime.messageText[message.ProviderItemID] = message.Text
			}
		}
		for _, activity := range m.store.SideActivities(side.ID, []string{active.ID}) {
			key := activity.ProviderItemID
			if activity.RequestID != "" {
				key = "request:" + activity.RequestID
			}
			if key != "" {
				runtime.activityIDs[key] = activity.ID
				runtime.activityText[key] = activity.Text
			}
		}
	}
	runtime.side.State = "ready"
	m.runtimes[side.ID] = runtime
	m.mu.Unlock()
	go m.consumeEvents(ctx, runtime)
	if active != nil {
		go func() {
			m.awaitSideTurn(ctx, runtime, side, *active, active.ProviderTurnID)
			runtime.mu.Lock()
			runtime.activeTurnID = ""
			runtime.mu.Unlock()
			m.signal()
		}()
	}
	m.signal()
	return true
}

func (m *sideManager) consumeEvents(ctx context.Context, runtime *sideRuntime) {
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-runtime.conv.Events():
			if !ok {
				unexpected := ctx.Err() == nil && m.ctx.Err() == nil
				runtime.cancel()
				m.mu.Lock()
				if m.runtimes[runtime.side.ID] == runtime {
					delete(m.runtimes, runtime.side.ID)
				}
				m.mu.Unlock()
				if unexpected {
					_ = m.store.SetSideFailed(context.Background(), runtime.side.ID, runtime.side.Generation,
						"Side provider connection ended; close and reopen the side chat.", m.service.now())
					m.announce(runtime.side.ID)
				}
				return
			}
			switch event.Kind {
			case ports.ChatEventMessageDelta, ports.ChatEventMessageCompleted:
				m.projectSideMessage(runtime, event)
			case ports.ChatEventActivityStarted, ports.ChatEventActivityCompleted,
				ports.ChatEventCommandOutputDelta, ports.ChatEventActivityText,
				ports.ChatEventApprovalRequested, ports.ChatEventApprovalResolved,
				ports.ChatEventInputRequested, ports.ChatEventInputResolved:
				m.projectSideActivity(runtime, event)
			case ports.ChatEventTurnCompleted:
				runtime.mu.Lock()
				noActiveTurn := runtime.activeTurnID == ""
				compactionCompleted := noActiveTurn && runtime.compacting
				runtime.mu.Unlock()
				if compactionCompleted {
					// ACP hosts refuse another prompt until the terminal receipt is
					// acknowledged, including receipts for compaction-only turns.
					if acknowledger, ok := runtime.conv.(ports.ChatProviderEventAcknowledger); ok && event.ProviderEventID != "" {
						ackCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
						err := acknowledger.AcknowledgeProviderEvent(ackCtx, event.ProviderEventID)
						cancel()
						if err != nil {
							runtime.mu.Lock()
							runtime.dispatchBlocked = true
							runtime.mu.Unlock()
							m.failOpen(runtime.side, fmt.Errorf("side provider did not acknowledge compaction; close and reopen the side chat: %w", err))
						}
					}
					runtime.mu.Lock()
					runtime.compacting = false
					runtime.mu.Unlock()
					m.signal()
				}
				if noActiveTurn {
					continue
				}
				select {
				case runtime.completed <- event:
				case <-ctx.Done():
					return
				}
			}
		}
	}
}

func (m *sideManager) projectSideActivity(runtime *sideRuntime, event ports.ChatEvent) {
	runtime.mu.Lock()
	turnID := runtime.activeTurnID
	if turnID == "" {
		runtime.mu.Unlock()
		return
	}
	key := event.ProviderItemID
	if event.RequestID != "" {
		key = "request:" + event.RequestID
	}
	if key == "" {
		runtime.mu.Unlock()
		return
	}
	id := runtime.activityIDs[key]
	if id == "" {
		id = m.service.newID()
		runtime.activityIDs[key] = id
	}
	text := runtime.activityText[key]
	if event.Kind == ports.ChatEventCommandOutputDelta || event.Kind == ports.ChatEventActivityText {
		text += event.Delta
	}
	if event.Text != "" {
		text = event.Text
	}
	runtime.activityText[key] = text
	runtime.mu.Unlock()
	activity := domain.SideActivity{ID: id, SideID: runtime.side.ID, TurnID: turnID,
		ProviderItemID: event.ProviderItemID, Summary: event.Summary, Text: text,
		RequestID: event.RequestID, CreatedAt: m.service.now(), Status: "running", Kind: "system",
		Detail: append([]byte(nil), event.Detail...)}
	if event.ActivityKind != "" {
		activity.Kind = string(event.ActivityKind)
	}
	if event.ActivityStatus != "" {
		activity.Status = string(event.ActivityStatus)
	}
	switch event.Kind {
	case ports.ChatEventApprovalRequested, ports.ChatEventApprovalResolved:
		activity.Kind = "approval"
		activity.Status = "pending"
		if event.Kind == ports.ChatEventApprovalResolved {
			activity.Status = "completed"
		}
		for _, decision := range event.Decisions {
			activity.Decisions = append(activity.Decisions,
				domain.SideDecision{ID: decision.ID, Label: decision.Label, Kind: string(decision.Kind), Raw: decision.Raw})
		}
	case ports.ChatEventInputRequested, ports.ChatEventInputResolved:
		activity.Kind = "user_input"
		activity.Status = "pending"
		if event.Kind == ports.ChatEventInputResolved {
			activity.Status = "completed"
		}
		if event.Input != nil {
			activity.Input = &domain.SideInput{Mode: string(event.Input.Mode), Message: event.Input.Message, URL: event.Input.URL, Schema: event.Input.Schema}
		}
	case ports.ChatEventActivityCompleted:
		activity.Status = "completed"
	}
	if err := m.store.UpsertSideActivity(runtime.side.ID, runtime.side.Generation, activity); err != nil {
		m.service.log.Warn("side activity projection failed", "side", runtime.side.ID, "error", err)
	}
	m.announce(runtime.side.ID)
}

func (m *sideManager) projectSideMessage(runtime *sideRuntime, event ports.ChatEvent) {
	if event.ProviderItemID == "" {
		return
	}
	runtime.mu.Lock()
	turnID := runtime.activeTurnID
	if turnID == "" || (runtime.providerTurnID != "" && event.ProviderTurnID != runtime.providerTurnID) {
		runtime.mu.Unlock()
		return
	}
	text := runtime.messageText[event.ProviderItemID]
	if event.Kind == ports.ChatEventMessageDelta {
		text += event.Delta
	} else if event.Text != "" {
		text = event.Text
	}
	runtime.messageText[event.ProviderItemID] = text
	id := runtime.messageIDs[event.ProviderItemID]
	if id == "" {
		id = m.service.newID()
		runtime.messageIDs[event.ProviderItemID] = id
	}
	runtime.mu.Unlock()
	now := m.service.now()
	if err := m.store.UpsertSideMessage(context.Background(), domain.SideMessage{
		ID: id, SideID: runtime.side.ID, TurnID: turnID,
		ProviderItemID: event.ProviderItemID, Role: "assistant", Text: text,
		Streaming: event.Kind == ports.ChatEventMessageDelta, CreatedAt: now, UpdatedAt: now,
	}, runtime.side.Generation); err != nil {
		m.service.log.Warn("side message projection failed", "side", runtime.side.ID, "error", err)
	}
	m.announce(runtime.side.ID)
}

func (m *sideManager) signal() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}
