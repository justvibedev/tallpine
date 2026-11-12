package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// ErrClearWithText refuses a message that opens with "/clear" but holds more.
// Claude Code would run it as /clear with arguments, a native clear that drops
// the task brief; only the bare command clears, and Tallpine runs that one.
var ErrClearWithText = errors.New("send /clear on its own to clear the context") //nolint:staticcheck // user-facing copy

// isClearCommand reports whether a message is exactly the "/clear" command.
func isClearCommand(msg ports.ChatUserMessage) bool {
	return strings.TrimSpace(msg.Text) == "/clear" && len(msg.Content) == 0
}

// opensWithClear reports a message whose first word is "/clear", the bare
// command included. A "/clear" later in the text is ordinary prose.
func opensWithClear(msg ports.ChatUserMessage) bool {
	fields := strings.Fields(msg.Text)
	return len(fields) > 0 && fields[0] == "/clear"
}

// refusedClear is the refusal of a "/clear" with more in the message, for an
// agent that would clear natively on it. Codex reads such a message as prompt
// text, so it goes through.
func refusedClear(record domain.SessionRecord, msg ports.ChatUserMessage) error {
	if record.Harness == domain.HarnessClaudeCode && opensWithClear(msg) && !isClearCommand(msg) {
		return ErrClearWithText
	}
	return nil
}

// interceptsClear reports a "/clear" Tallpine carries out itself (see
// clearsByHandoff). Send, Steer and SteerOrSend all share it so no delivery
// path forwards the text.
func interceptsClear(record domain.SessionRecord, msg ports.ChatUserMessage) bool {
	return clearsByHandoff(record.Harness) && isClearCommand(msg)
}

// Send delivers a message to a session's agent.
func (s *Service) Send(
	ctx context.Context,
	id domain.SessionID,
	msg ports.ChatUserMessage,
) (domain.ConversationTurn, error) {
	record, err := s.requireChatSession(ctx, id)
	if err != nil {
		return domain.ConversationTurn{}, err
	}
	if err := refusedClear(record, msg); err != nil {
		return domain.ConversationTurn{}, err
	}
	if interceptsClear(record, msg) {
		marker, err := s.clearByHandoff(ctx, id)
		if err != nil {
			return domain.ConversationTurn{}, err
		}
		return domain.ConversationTurn{ContextClearedActivityID: marker}, nil
	}
	if s.switchBeforeSend(ctx, record) {
		// The provider stopped on its old release. The hibernated path below
		// queues this message and wakes the session on the installed one.
		if record, err = s.requireChatSession(ctx, id); err != nil {
			return domain.ConversationTurn{}, err
		}
	}
	if msg.ClientMessageID != "" && len(msg.Excerpts) > 0 {
		if controller, controllerErr := s.Controller(id); controllerErr == nil {
			if existing, found, lookupErr := controller.store.ConversationMessageByClientID(ctx, controller.conversation.ID, msg.ClientMessageID); lookupErr != nil {
				return domain.ConversationTurn{}, fmt.Errorf("read message delivery: %w", lookupErr)
			} else if found {
				msg.ClientPayloadHash = existing.ClientPayloadHash
				return controller.Send(ctx, msg)
			}
		}
	}
	msg.ClientPayloadHash, err = clientPayloadHash(msg)
	if err != nil {
		return domain.ConversationTurn{}, err
	}
	var turn domain.ConversationTurn
	if record.ProvisionState.IsProvisioning() || record.HibernatedAt != nil {
		// Keep messages on the durable queue until provisioning finishes so a
		// new message cannot overtake the opening prompt during handoff.
		turn, err = s.queueWithoutController(ctx, record, msg)
		if errors.Is(err, ErrNotProvisioning) {
			// Startup may have become ready after the first read. The store
			// refused a stale queue append; hand the message to its controller.
			latest, readErr := s.requireChatSession(ctx, id)
			if readErr == nil && !latest.IsTerminated && latest.ProvisionState.WithDefault() == domain.SessionProvisionReady {
				var controller *Controller
				var release func()
				controller, release, err = s.workingController(ctx, id)
				if err == nil {
					if hydrateErr := hydrateExcerptReferences(ctx, controller, &msg); hydrateErr != nil {
						err = hydrateErr
					} else {
						turn, err = controller.Send(ctx, msg)
					}
					release()
				}
			}
		}
		if err == nil && record.HibernatedAt != nil && turn.ID != "" {
			s.startBackgroundWake(id)
		}
	} else {
		var controller *Controller
		var release func()
		controller, release, err = s.workingController(ctx, id)
		if err == nil {
			if hydrateErr := hydrateExcerptReferences(ctx, controller, &msg); hydrateErr != nil {
				err = hydrateErr
			} else {
				turn, err = controller.Send(ctx, msg)
			}
			release()
		} else if queued, ok := s.queueAfterFailedWake(ctx, id, msg, err); ok {
			turn, err = queued, nil
		}
	}
	return turn, err
}

// queueAfterFailedWake keeps a message that joined a failed wake. The send
// arrived while the session was starting, so it is accepted like a message to
// a sleeping session: queued durably and delivered once a retried wake
// succeeds, instead of failing because the first start attempt did.
func (s *Service) queueAfterFailedWake(ctx context.Context, id domain.SessionID, msg ports.ChatUserMessage, wakeErr error) (domain.ConversationTurn, bool) {
	resumeFailed := errors.Is(wakeErr, ports.ErrChatResumeFailed) ||
		(errors.Is(wakeErr, context.DeadlineExceeded) && ctx.Err() == nil)
	if !resumeFailed || s.HasLiveChatController(id) || !s.scheduleWakeRetry(ctx, id) {
		return domain.ConversationTurn{}, false
	}
	record, err := s.requireChatSession(ctx, id)
	if err != nil || record.IsTerminated {
		return domain.ConversationTurn{}, false
	}
	if afterFailedWakeRead != nil {
		afterFailedWakeRead()
	}
	var turn domain.ConversationTurn
	if record.HibernatedAt != nil {
		turn, err = s.queueWithoutController(ctx, record, msg)
	}
	if record.HibernatedAt == nil || errors.Is(err, ErrNotProvisioning) {
		// A concurrent wake started the provider meanwhile; hand it the message.
		if !s.HasLiveChatController(id) {
			return domain.ConversationTurn{}, false
		}
		controller, release, ctrlErr := s.workingController(ctx, id)
		if ctrlErr != nil {
			return domain.ConversationTurn{}, false
		}
		defer release()
		if err := hydrateExcerptReferences(ctx, controller, &msg); err != nil {
			return domain.ConversationTurn{}, false
		}
		turn, err = controller.Send(ctx, msg)
		return turn, err == nil
	}
	if err != nil {
		return domain.ConversationTurn{}, false
	}
	// Wake now rather than at the next retry tick. If a concurrent wake
	// already finished its drain before this append, the drain that follows
	// this wake (which joins or reuses it) still delivers the message.
	s.startBackgroundWake(id)
	return turn, true
}

// afterFailedWakeRead lets tests interleave a concurrent wake between reading
// the session and queueing the message.
var afterFailedWakeRead func()

// SendForOwner sends a user message to an owner-specific chat controller.
func (s *Service) SendForOwner(ctx context.Context, owner domain.ConversationOwner, msg ports.ChatUserMessage) (domain.ConversationTurn, error) {
	controller, err := s.ControllerForOwner(owner)
	if err != nil {
		return domain.ConversationTurn{}, err
	}
	// A crash-safe retry may arrive after its source message changed. Once this
	// client id is already durable, the controller's existing idempotency path is
	// authoritative and must not be blocked by re-validating old selection text.
	if msg.ClientMessageID != "" && len(msg.Excerpts) > 0 {
		if existing, found, lookupErr := controller.store.ConversationMessageByClientID(
			ctx, controller.conversation.ID, msg.ClientMessageID,
		); lookupErr != nil {
			return domain.ConversationTurn{}, fmt.Errorf("read message delivery: %w", lookupErr)
		} else if found {
			msg.ClientPayloadHash = existing.ClientPayloadHash
			return controller.Send(ctx, msg)
		}
	}
	if err := hydrateExcerptReferences(ctx, controller, &msg); err != nil {
		return domain.ConversationTurn{}, err
	}
	return controller.Send(ctx, msg)
}

func hydrateExcerptReferences(ctx context.Context, controller *Controller, msg *ports.ChatUserMessage) error {
	if len(msg.Excerpts) == 0 {
		return nil
	}
	if err := validateExcerptReferences(msg.Excerpts, controller.conversation.ID); err != nil {
		return err
	}
	messages, err := controller.store.ConversationMessages(ctx, controller.conversation.ID)
	if err != nil {
		return fmt.Errorf("read excerpt transcript: %w", err)
	}
	for _, excerpt := range msg.Excerpts {
		text := strings.TrimSpace(excerpt.Text)
		var source domain.ConversationMessage
		found := false
		for _, candidate := range messages {
			if candidate.ID == excerpt.MessageID {
				source, found = candidate, true
				break
			}
		}
		if !found || source.Revision != excerpt.Revision || source.Streaming || source.TurnID == "" || !sourceContainsExcerptSelection(source, text) || (source.Role != domain.MessageRoleUser && source.Role != domain.MessageRoleAssistant) {
			return fmt.Errorf("%w: source message changed or no longer contains the selection; reselect it", ErrExcerptStale)
		}
		turn, err := controller.store.TurnByID(ctx, source.TurnID)
		if err != nil || turn.ConversationID != controller.conversation.ID || turn.State != domain.TurnStateCompleted || turn.RolledBackAt != nil {
			return fmt.Errorf("%w: source turn is not complete", ErrExcerptStale)
		}
		var userText, assistantText string
		for _, candidate := range messages {
			if candidate.TurnID != source.TurnID || candidate.Streaming {
				continue
			}
			switch candidate.Role {
			case domain.MessageRoleUser:
				if userText != "" {
					userText += "\n\n"
				}
				userText += candidate.Text
			case domain.MessageRoleAssistant:
				if assistantText != "" {
					assistantText += "\n\n"
				}
				assistantText += candidate.Text
			}
		}
		if userText == "" || assistantText == "" {
			return fmt.Errorf("%w: source turn has no complete user and assistant messages", ErrExcerptStale)
		}
		msg.Content = append(msg.Content, ports.ChatContent{
			Type: "excerpt", URI: ports.ChatExcerptResourceURIPrefix + source.ID,
			Name: fmt.Sprintf("%s message", source.Role), MIMEType: "text/plain", Text: text,
			Excerpt: &ports.ChatExcerptContext{
				Reference: excerpt, SelectedText: text,
				UserMessage:      truncateExcerptContext(userText),
				AssistantMessage: truncateExcerptContext(assistantText),
			},
		})
	}
	msg.Excerpts = nil
	return nil
}

// validateExcerptReferences checks what can be checked without the transcript,
// so a send queued before its controller starts is bounded the same way.
func validateExcerptReferences(excerpts []ports.ChatExcerptReference, conversationID string) error {
	if len(excerpts) > maxExcerptReferences {
		return fmt.Errorf("%w: at most %d excerpts may be attached", ErrExcerptInvalid, maxExcerptReferences)
	}
	total := 0
	for _, excerpt := range excerpts {
		text := strings.TrimSpace(excerpt.Text)
		if excerpt.ConversationID != conversationID || excerpt.MessageID == "" || text == "" || excerpt.Revision < 0 {
			return fmt.Errorf("%w: source conversation, message, revision, and text are required", ErrExcerptInvalid)
		}
		total += len(text)
		if total > maxExcerptTextBytes {
			return fmt.Errorf("%w: attached excerpts exceed %d bytes", ErrExcerptInvalid, maxExcerptTextBytes)
		}
	}
	return nil
}

func truncateExcerptContext(text string) string {
	if len(text) <= maxExcerptPairedTextBytes {
		return text
	}
	return truncateUTF8Head(text, maxExcerptPairedTextBytes) + "\n[truncated]"
}
