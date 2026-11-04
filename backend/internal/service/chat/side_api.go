package chat

import (
	"context"
	"fmt"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// SideSnapshot returns a paginated side transcript.
func (s *Service) SideSnapshot(ctx context.Context, session domain.SessionID, sideID string, before time.Time, limit int) (domain.SideSnapshot, error) {
	if s.sides == nil {
		return domain.SideSnapshot{}, ErrSideUnavailable
	}
	side, err := s.sides.ownedSide(ctx, session, sideID)
	if err != nil {
		return domain.SideSnapshot{}, err
	}
	if side.State == "ready" {
		if err := s.sides.ensureRestore(side); err != nil {
			s.sides.failOpen(side, err)
		}
	}
	turns, more, err := s.sides.store.SideTurns(ctx, sideID, before, limit)
	if err != nil {
		return domain.SideSnapshot{}, err
	}
	ids := make([]string, 0, len(turns))
	for _, turn := range turns {
		ids = append(ids, turn.ID)
	}
	messages, err := s.sides.store.SideMessages(ctx, sideID, ids)
	if err != nil {
		return domain.SideSnapshot{}, err
	}
	return domain.SideSnapshot{Side: side, Turns: turns, Messages: messages,
		Activities: s.sides.store.SideActivities(sideID, ids), HasMore: more}, nil
}

// ResolveSideApproval forwards an approval decision to the side provider.
func (s *Service) ResolveSideApproval(ctx context.Context, session domain.SessionID, sideID, requestID, decisionID string) error {
	if s.sides == nil {
		return ErrSideUnavailable
	}
	if _, err := s.sides.ownedSide(ctx, session, sideID); err != nil {
		return err
	}
	activity, err := s.sides.store.SideApproval(sideID, requestID)
	if err != nil {
		return err
	}
	var decision ports.ChatDecision
	for _, option := range activity.Decisions {
		if option.ID == decisionID {
			decision = ports.ChatDecision{ID: option.ID, Raw: option.Raw}
			break
		}
	}
	if decision.ID == "" {
		return ErrSideQuestionInvalid
	}
	s.sides.mu.Lock()
	runtime := s.sides.runtimes[sideID]
	s.sides.mu.Unlock()
	if runtime == nil {
		return ErrSideUnavailable
	}
	return runtime.conv.ResolveRequest(ctx, requestID, decision)
}

// ResolveSideInput forwards requested input to the side provider.
func (s *Service) ResolveSideInput(ctx context.Context, session domain.SessionID, sideID, requestID string, response ports.ChatInputResponse) error {
	if s.sides == nil {
		return ErrSideUnavailable
	}
	if _, err := s.sides.ownedSide(ctx, session, sideID); err != nil {
		return err
	}
	if _, err := s.sides.store.SideInput(sideID, requestID); err != nil {
		return err
	}
	if !response.Action.Valid() {
		return ErrSideQuestionInvalid
	}
	s.sides.mu.Lock()
	runtime := s.sides.runtimes[sideID]
	s.sides.mu.Unlock()
	if runtime == nil {
		return ErrSideUnavailable
	}
	responder, ok := runtime.conv.(ports.ChatInputResponder)
	if !ok {
		return ErrSideProviderUnsupported
	}
	return responder.ResolveInput(ctx, requestID, response)
}

func (m *sideManager) ownedSide(ctx context.Context, session domain.SessionID, sideID string) (domain.SideConversation, error) {
	side, err := m.store.SideConversation(ctx, sideID)
	if err != nil {
		return side, err
	}
	record, err := m.service.requireChatSession(ctx, session)
	if err != nil {
		return side, err
	}
	if record.IsTerminated {
		return side, ErrSideUnavailable
	}
	if side.SessionID != session || side.AppRunID != m.launchID() || side.ClosedAt != nil {
		return side, ErrSideUnavailable
	}
	return side, nil
}

// SendSideQuestion accepts a question for the side provider.
func (s *Service) SendSideQuestion(ctx context.Context, session domain.SessionID, sideID string, msg ports.ChatUserMessage) (domain.SideTurn, error) {
	if s.sides == nil {
		return domain.SideTurn{}, ErrSideUnavailable
	}
	side, err := s.sides.ownedSide(ctx, session, sideID)
	if err != nil {
		return domain.SideTurn{}, err
	}
	if side.State == "ready" {
		if err := s.sides.ensureRestore(side); err != nil {
			s.sides.failOpen(side, err)
		}
	}
	text := msg.Text
	if (strings.TrimSpace(text) == "" && len(msg.Content) == 0 && len(msg.Excerpts) == 0) || len(text) > 128*1024 || strings.TrimSpace(msg.ClientMessageID) == "" {
		return domain.SideTurn{}, ErrSideQuestionInvalid
	}
	if strings.TrimSpace(text) == "" && len(msg.Excerpts) > 0 {
		text = fmt.Sprintf("Use the attached %d chat excerpt(s) as context", len(msg.Excerpts))
	} else if strings.TrimSpace(text) == "" {
		text = fmt.Sprintf("Attached %d item(s) for context", len(msg.Content))
	}
	// A retry with the same receipt uses its frozen references even if the source
	// has since changed. The store still checks the complete request for conflict.
	previous, _, err := s.sides.store.SideTurns(ctx, sideID, time.Time{}, 0)
	if err != nil {
		return domain.SideTurn{}, err
	}
	for _, turn := range previous {
		if turn.ClientMessageID == msg.ClientMessageID {
			if turn.Text != text || len(turn.References) != len(msg.Excerpts) || len(turn.Content) != len(msg.Content) {
				return domain.SideTurn{}, ErrSideIdempotencyConflict
			}
			for i, item := range msg.Content {
				frozen := turn.Content[i]
				if frozen.Type != item.Type || frozen.MIMEType != item.MIMEType || frozen.Data != item.Data || frozen.URI != item.URI || frozen.Name != item.Name || frozen.Text != item.Text {
					return domain.SideTurn{}, ErrSideIdempotencyConflict
				}
			}
			for i, ref := range msg.Excerpts {
				frozen := turn.References[i]
				if frozen.ConversationID != ref.ConversationID || frozen.MessageID != ref.MessageID || frozen.Revision != ref.Revision || frozen.Selection != ref.Text {
					return domain.SideTurn{}, ErrSideIdempotencyConflict
				}
			}
			return turn, nil
		}
	}
	references, err := s.validateSideReferences(ctx, session, side, msg.Excerpts)
	if err != nil {
		return domain.SideTurn{}, err
	}
	if side.State != "ready" && side.State != "opening" {
		return domain.SideTurn{}, ErrSideNotReady
	}
	s.sides.mu.Lock()
	connected := s.sides.runtimes[sideID] != nil
	s.sides.mu.Unlock()
	if !connected && side.State != "opening" {
		return domain.SideTurn{}, ErrSideNotReady
	}
	content := make([]domain.SideContent, 0, len(msg.Content))
	for _, item := range msg.Content {
		content = append(content, domain.SideContent{
			Type: item.Type, MIMEType: item.MIMEType, Data: item.Data, URI: item.URI, Name: item.Name, Text: item.Text})
	}
	turn, created, err := s.sides.store.ReserveSideTurn(ctx, domain.SideTurn{
		ID: s.newID(), SideID: sideID, ClientMessageID: msg.ClientMessageID, Text: text, Content: content,
		References: references, CreatedAt: s.now()}, s.sides.launchID())
	if err != nil {
		return domain.SideTurn{}, err
	}
	if created {
		s.sides.signal()
		s.sides.announce(sideID)
	}
	return turn, nil
}

// EditQueuedSideQuestion replaces the text of a queued side question.
func (s *Service) EditQueuedSideQuestion(ctx context.Context, session domain.SessionID, sideID, turnID, text string) error {
	if s.sides == nil {
		return ErrSideUnavailable
	}
	if _, err := s.sides.ownedSide(ctx, session, sideID); err != nil {
		return err
	}
	if strings.TrimSpace(text) == "" {
		return ErrSideQuestionInvalid
	}
	if err := s.sides.store.EditQueuedSideTurn(ctx, sideID, turnID, text); err != nil {
		return err
	}
	s.sides.announce(sideID)
	return nil
}

// RetrySideQuestion requeues a failed side question.
func (s *Service) RetrySideQuestion(ctx context.Context, session domain.SessionID, sideID, turnID string) error {
	if s.sides == nil {
		return ErrSideUnavailable
	}
	side, err := s.sides.ownedSide(ctx, session, sideID)
	if err != nil {
		return err
	}
	if side.PolicyVersion != sidePolicyVersion || side.RecreateRequired || side.BoundaryState == "pending" || side.BoundaryState == "uncertain" {
		return ErrSidePolicyRecreate
	}
	s.sides.mu.Lock()
	runtime := s.sides.runtimes[sideID]
	s.sides.mu.Unlock()
	if runtime != nil {
		runtime.mu.Lock()
		runtime.dispatchBlocked = false
		runtime.mu.Unlock()
	}
	if err := s.sides.store.RetrySideTurn(ctx, sideID, turnID); err != nil {
		return err
	}
	s.sides.signal()
	s.sides.announce(sideID)
	return nil
}

// UpdateSideSettings updates model and effort for a side chat.
func (s *Service) UpdateSideSettings(ctx context.Context, session domain.SessionID, sideID, model, effort string) error {
	if s.sides == nil {
		return ErrSideUnavailable
	}
	if _, err := s.sides.ownedSide(ctx, session, sideID); err != nil {
		return err
	}
	// A side chat runs the model it is given, so the project's model policy
	// applies here as it does to the main chat.
	if err := s.checkModelPolicy(ctx, session, model); err != nil {
		return err
	}
	if err := s.sides.store.UpdateSideSettings(ctx, sideID, model, effort, s.now()); err != nil {
		return err
	}
	s.sides.announce(sideID)
	return nil
}

// SaveSideDraft keeps a side draft in launch-scoped memory.
func (s *Service) SaveSideDraft(ctx context.Context, session domain.SessionID, sideID, contentJSON string) error {
	if s.sides == nil {
		return ErrSideUnavailable
	}
	if _, err := s.sides.ownedSide(ctx, session, sideID); err != nil {
		return err
	}
	// Receipts may contain the exact supported image payload for an uncertain send.
	if len(contentJSON) > 40<<20 {
		return ErrSideDraftTooLarge
	}
	if err := s.sides.store.SetSideDraft(ctx, sideID, s.sides.launchID(), contentJSON, s.now()); err != nil {
		return err
	}
	s.sides.announce(sideID)
	return nil
}

// SideDraft returns a side draft from launch-scoped memory.
func (s *Service) SideDraft(ctx context.Context, session domain.SessionID, sideID string) (string, error) {
	if s.sides == nil {
		return "", ErrSideUnavailable
	}
	if _, err := s.sides.ownedSide(ctx, session, sideID); err != nil {
		return "", err
	}
	return s.sides.store.SideDraft(ctx, sideID)
}

// CompactSideChat asks the side's own provider conversation to compact. It
// never operates on the main chat controller or provider handle.
func (s *Service) CompactSideChat(ctx context.Context, session domain.SessionID, sideID string) (ports.ChatCompactionResult, error) {
	if s.sides == nil {
		return ports.ChatCompactionResult{}, ErrSideUnavailable
	}
	if _, err := s.sides.ownedSide(ctx, session, sideID); err != nil {
		return ports.ChatCompactionResult{}, err
	}
	s.sides.mu.Lock()
	runtime := s.sides.runtimes[sideID]
	s.sides.mu.Unlock()
	if runtime == nil {
		return ports.ChatCompactionResult{}, ErrSideUnavailable
	}
	compactor, ok := runtime.conv.(ports.ChatCompactor)
	if !ok || !runtime.conv.Capabilities().Has(ports.ChatCapabilityCompaction) {
		return ports.ChatCompactionResult{}, ErrCompactionUnsupported
	}
	runtime.mu.Lock()
	if runtime.activeTurnID != "" || runtime.compacting {
		runtime.mu.Unlock()
		return ports.ChatCompactionResult{}, ErrCompactionWhileBusy
	}
	runtime.compacting = true
	runtime.mu.Unlock()
	result, err := compactor.Compact(ctx)
	if err != nil {
		runtime.mu.Lock()
		runtime.compacting = false
		runtime.mu.Unlock()
		s.sides.signal()
		return ports.ChatCompactionResult{}, err
	}
	return result, nil
}

// InterruptSideQuestion interrupts the active side question.
func (s *Service) InterruptSideQuestion(ctx context.Context, session domain.SessionID, sideID string) error {
	if s.sides == nil {
		return ErrSideUnavailable
	}
	if _, err := s.sides.ownedSide(ctx, session, sideID); err != nil {
		return err
	}
	s.sides.mu.Lock()
	runtime := s.sides.runtimes[sideID]
	s.sides.mu.Unlock()
	if runtime == nil {
		return ErrSideUnavailable
	}
	runtime.mu.Lock()
	// Only a turn the provider is running can be stopped. Recording a Stop for
	// one still being dispatched would leave the flag to excuse a later failure.
	stopping := runtime.activeTurnID != "" && runtime.providerTurnID != ""
	if stopping {
		runtime.stopRequested = true
	}
	runtime.mu.Unlock()
	if err := runtime.conv.Interrupt(ctx, ""); err != nil {
		if stopping {
			runtime.mu.Lock()
			runtime.stopRequested = false
			runtime.mu.Unlock()
		}
		return err
	}
	return nil
}
