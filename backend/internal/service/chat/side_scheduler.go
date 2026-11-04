package chat

import (
	"context"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func (m *sideManager) schedule() {
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-m.wake:
		}
		for m.ctx.Err() == nil {
			m.mu.Lock()
			excluded := make(map[string]bool)
			sides, err := m.store.ListOpenSidesForRun(m.ctx, m.runID)
			if err != nil {
				m.mu.Unlock()
				break
			}
			for _, side := range sides {
				runtime := m.runtimes[side.ID]
				if runtime == nil {
					excluded[side.ID] = true
					continue
				}
				runtime.mu.Lock()
				excluded[side.ID] = runtime.dispatchBlocked || runtime.compacting || runtime.activeTurnID != ""
				runtime.mu.Unlock()
			}
			turn, side, found, err := m.store.ClaimNextSideTurn(m.ctx, m.runID, excluded, m.service.now())
			if err != nil || !found {
				m.mu.Unlock()
				break
			}
			runtime := m.runtimes[side.ID]
			runtime.mu.Lock()
			if runtime.compacting || runtime.activeTurnID != "" {
				runtime.mu.Unlock()
				_ = m.store.RequeueSideTurn(m.ctx, side.ID, turn.ID)
				m.mu.Unlock()
				continue
			}
			runtime.activeTurnID = turn.ID
			runtime.stopRequested = false
			runtime.mu.Unlock()
			m.mu.Unlock()
			go func() {
				defer func() {
					runtime.mu.Lock()
					if runtime.activeTurnID == turn.ID {
						runtime.activeTurnID = ""
					}
					runtime.mu.Unlock()
					m.signal()
				}()
				m.runTurn(runtime, side, turn)
			}()
		}
	}
}

func (m *sideManager) runTurn(runtime *sideRuntime, side domain.SideConversation, turn domain.SideTurn) {
	ctx := runtime.ctx
	if ctx == nil {
		ctx = m.ctx
	}
	runtime.mu.Lock()
	for runtime.compacting {
		runtime.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-time.After(25 * time.Millisecond):
		}
		runtime.mu.Lock()
	}
	runtime.activeTurnID = turn.ID
	runtime.providerTurnID = ""
	runtime.messageText = make(map[string]string)
	runtime.messageIDs = make(map[string]string)
	runtime.mu.Unlock()
	if len(turn.References) > 0 && turn.RetryOfTurnID == "" {
		refs := make([]ports.ChatExcerptReference, 0, len(turn.References))
		for _, ref := range turn.References {
			refs = append(refs, ports.ChatExcerptReference{ConversationID: ref.ConversationID, MessageID: ref.MessageID, Revision: ref.Revision, Text: ref.Selection})
		}
		if _, err := m.service.validateSideReferences(ctx, side.SessionID, side, refs); err != nil {
			reason := err.Error()
			if errors.Is(err, ErrExcerptStale) || errors.Is(err, ErrExcerptInvalid) {
				reason = "chat excerpt is stale"
			} else {
				runtime.mu.Lock()
				runtime.dispatchBlocked = true
				runtime.mu.Unlock()
			}
			if settleErr := m.store.SettleSideTurn(ctx, side.ID, turn.ID, side.Generation, "failed", "", reason, m.service.now()); settleErr != nil {
				runtime.mu.Lock()
				runtime.dispatchBlocked = true
				runtime.mu.Unlock()
			}
			m.service.log.Warn("queued side excerpt validation failed", "session", side.SessionID, "side", side.ID, "turn", turn.ID, "error", err)
			m.announce(side.ID)
			return
		}
	}
	m.announce(side.ID)
	text := sideQuestionWithReferences(turn)
	// Keep attachment paths and resource contents out of the user's wording while
	// retaining a text representation for providers without resource blocks.
	for _, item := range turn.Content {
		if item.Type == "resource" || item.Type == "resource_link" {
			text += fmt.Sprintf("\n\nAttached resource (background data): %s\n%s\n%s", item.Name, item.URI, item.Text)
		}
	}
	firstDelivery, err := m.store.PrepareSideBoundary(ctx, side.ID, turn.ID, side.Generation)
	if err != nil {
		_ = m.store.SettleSideTurn(ctx, side.ID, turn.ID, side.Generation, "failed", "", err.Error(), m.service.now())
		m.failOpen(side, err)
		return
	}
	if firstDelivery {
		text = sideConversationBoundary + "\n\nCurrent side-chat request:\n" + text
		if side.ContextMode == "reconstructed" && side.SeedHistory != "" {
			text = "Recorded visible main-chat history through the frozen anchor (reference data):\n" + side.SeedHistory + "\n\n" + text
		}
	}
	content := make([]ports.ChatContent, 0, len(turn.Content))
	for _, item := range turn.Content {
		content = append(content, ports.ChatContent{
			Type: item.Type, MIMEType: item.MIMEType, Data: item.Data, URI: item.URI, Name: item.Name, Text: item.Text})
	}
	providerRequestID := turn.ClientMessageID
	if turn.DispatchAttempt > 0 {
		providerRequestID = fmt.Sprintf("%s:retry:%d", turn.ClientMessageID, turn.DispatchAttempt)
	}
	ref, err := runtime.conv.SendTurn(ctx, ports.ChatUserMessage{Text: text, Content: content,
		ClientMessageID: providerRequestID, Origin: domain.MessageOriginHuman,
		Settings: ports.ChatTurnSettings{Model: side.Model, Effort: side.Effort}})
	if err != nil {
		if firstDelivery {
			m.failOpen(side, ErrSidePolicyRecreate)
		}
		_ = m.store.SettleSideTurn(context.Background(), side.ID, turn.ID, side.Generation,
			"failed", "", err.Error(), m.service.now())
		m.announce(side.ID)
		runtime.mu.Lock()
		runtime.dispatchBlocked = true
		runtime.activeTurnID = ""
		runtime.providerTurnID = ""
		runtime.mu.Unlock()
		return
	}
	runtime.mu.Lock()
	runtime.providerTurnID = ref.ProviderTurnID
	runtime.mu.Unlock()
	if err := m.store.SetSideTurnProviderID(ctx, side.ID, turn.ID, side.Generation, ref.ProviderTurnID); err != nil {
		_ = runtime.conv.Interrupt(ctx, ref.ProviderTurnID)
		if firstDelivery {
			m.failOpen(side, ErrSidePolicyRecreate)
		}
		_ = m.store.SettleSideTurn(context.Background(), side.ID, turn.ID, side.Generation,
			"failed", ref.ProviderTurnID, err.Error(), m.service.now())
		runtime.mu.Lock()
		runtime.dispatchBlocked = true
		runtime.activeTurnID = ""
		runtime.providerTurnID = ""
		runtime.mu.Unlock()
		m.announce(side.ID)
		return
	}
	// ACP prepares session/prompt in SendTurn and waits for an explicit start.
	// Bind the provider turn to this side before any streamed event can arrive.
	if deferred, ok := runtime.conv.(ports.ChatDeferredTurnStarter); ok {
		if err := deferred.StartDeferredTurn(ref.ProviderTurnID); err != nil {
			deferred.DiscardDeferredTurn(ref.ProviderTurnID)
			_ = m.store.SettleSideTurn(context.Background(), side.ID, turn.ID, side.Generation,
				"failed", ref.ProviderTurnID, err.Error(), m.service.now())
			m.announce(side.ID)
			runtime.mu.Lock()
			runtime.activeTurnID = ""
			runtime.providerTurnID = ""
			runtime.dispatchBlocked = true
			runtime.mu.Unlock()
			return
		}
	}
	m.awaitSideTurn(ctx, runtime, side, turn, ref.ProviderTurnID)
}

func (m *sideManager) awaitSideTurn(ctx context.Context, runtime *sideRuntime, side domain.SideConversation, turn domain.SideTurn, providerTurnID string) {
	for {
		select {
		case <-ctx.Done():
			if m.ctx.Err() == nil {
				_ = m.store.SettleSideTurn(context.Background(), side.ID, turn.ID, side.Generation,
					"failed", providerTurnID, "Side provider connection ended.", m.service.now())
				m.announce(side.ID)
			}
			return
		case <-runtime.done:
			_ = m.store.SettleSideTurn(context.Background(), side.ID, turn.ID, side.Generation,
				"failed", providerTurnID, "Side provider connection ended.", m.service.now())
			m.announce(side.ID)
			return
		case event := <-runtime.completed:
			if event.ProviderTurnID != providerTurnID {
				continue
			}
			state := "completed"
			message := ""
			if event.TurnState != domain.TurnStateCompleted {
				runtime.mu.Lock()
				stopped := runtime.stopRequested
				runtime.stopRequested = false
				// A provider failure holds the queue until the user retries, as in
				// the main chat. A turn the user stopped does not: later questions
				// must still dispatch, and the stopped one stays retryable.
				if !stopped {
					runtime.dispatchBlocked = true
				}
				runtime.mu.Unlock()
				state = "failed"
				if event.Err != nil {
					message = event.Err.Error()
				} else if stopped {
					message = "Stopped."
				}
			}
			if err := m.store.SettleSideTurn(context.Background(), side.ID, turn.ID, side.Generation,
				state, providerTurnID, message, m.service.now()); err != nil {
				m.service.log.Warn("side turn settlement failed", "side", side.ID, "turn", turn.ID, "error", err)
				return
			}
			if event.ProviderEventID != "" {
				if acknowledger, ok := runtime.conv.(ports.ChatProviderEventAcknowledger); ok {
					ackCtx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
					err := acknowledger.AcknowledgeProviderEvent(ackCtx, event.ProviderEventID)
					cancel()
					if err != nil && m.ctx.Err() == nil {
						m.service.log.Warn("side provider event acknowledgement failed", "side", side.ID, "turn", turn.ID, "error", err)
						_ = m.store.SetSideFailed(context.Background(), side.ID, side.Generation,
							"Side provider did not acknowledge the completed turn; close and reopen the side chat.", m.service.now())
					}
				}
			}
			m.announce(side.ID)
			runtime.mu.Lock()
			runtime.activeTurnID = ""
			runtime.providerTurnID = ""
			runtime.mu.Unlock()
			return
		}
	}
}

func (m *sideManager) stopAll() {
	m.cancel()
	m.mu.Lock()
	runtimes := m.runtimes
	m.runtimes = make(map[string]*sideRuntime)
	m.mu.Unlock()
	for _, runtime := range runtimes {
		runtime.cancel()
		if err := runtime.conv.Close(); err != nil {
			m.service.log.Warn("side detach failed", "side", runtime.side.ID, "error", err)
		}
	}
}
