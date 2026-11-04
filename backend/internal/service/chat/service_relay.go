package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	reportsvc "tallpine.eu/tallpine/backend/internal/service/report"
)

// RelayChatTurn delivers a message Tallpine is carrying for someone else.
//
// Origin is automation, not human: `tallpine send` and an orchestrator writing to a
// worker are Tallpine acting on the user's instructions, and the timeline attributes
// them so rather than passing them off as something the user typed here. The
// distinction is durable and structural — a reader must not have to infer it
// from a text prefix.
//
// Delivery follows the same rules as any other send: a message arriving mid-turn
// queues instead of racing the running turn.
func (s *Service) RelayChatTurn(ctx context.Context, id domain.SessionID, text string) (string, error) {
	return s.relayChatTurn(ctx, id, text, "", ports.MessageDeliveryOptions{})
}

// RelayChatTurnWithID is RelayChatTurn with a durable caller-supplied
// idempotency key. Interface-transition outbox retries use it so a crash after
// provider acceptance but before the outbox acknowledgement cannot create a
// duplicate Chat turn.
func (s *Service) RelayChatTurnWithID(
	ctx context.Context,
	id domain.SessionID,
	text, clientMessageID string,
) (string, error) {
	return s.relayChatTurn(ctx, id, text, clientMessageID, ports.MessageDeliveryOptions{})
}

// RelayUserAuthoredChatTurn delivers user-written content through Tallpine's relay
// path without changing its automation delivery attribution.
func (s *Service) RelayUserAuthoredChatTurn(ctx context.Context, id domain.SessionID, text string) (string, error) {
	return s.relayChatTurn(ctx, id, text, "", ports.MessageDeliveryOptions{AuthoredByUser: true})
}

func (s *Service) relayChatTurn(
	ctx context.Context,
	id domain.SessionID,
	text, clientMessageID string,
	options ports.MessageDeliveryOptions,
) (string, error) {
	turn, err := s.relayChatTurnReceipt(ctx, id, text, clientMessageID, options)
	return turn.ID, err
}

// steerRelayIntoBusyTurn hands a relayed message (an orchestrator's or the
// owner's `tallpine send`) to the turn the agent is running now, so guidance such
// as "hold, don't push" lands before the agent acts instead of after its turn
// ends. Only messages without a client message id take this path: idempotent
// retries and worker-originated relays (which carry a worker-send id) keep their
// own durable queue boundary. ok is false when the provider definitely did not
// take it (no steerable turn, a refusal, an interface handoff); the caller then
// queues it, the pre-steering behavior; so does a failure before dispatch (for
// example composing queued messages into the batch). A failed provider call is
// returned as ErrSteerDeliveryUncertain: the guidance may have reached the
// agent, and queuing it would deliver it twice.
func (s *Service) steerRelayIntoBusyTurn(ctx context.Context, id domain.SessionID, controller *Controller, msg ports.ChatUserMessage) (ports.MessageReceipt, bool, error) {
	// A steer pre-empts the running generation and aborts the in-flight tool call
	// (B491). Only a person (the owner or an orchestrator) is worth that;
	// Tallpine's own notices (review verdicts, [auto] lines) name no sender and
	// are not user-authored, so they wait at the turn boundary instead.
	if msg.SenderSessionID == "" && !msg.AuthoredByUser {
		return ports.MessageReceipt{}, false, nil
	}
	steered, err := controller.Steer(ctx, msg)
	switch {
	case err == nil:
		return ports.MessageReceipt{Outcome: ports.MessageDelivered, ID: steered.ActivityID}, true, nil
	case steered.ProviderTurnID != "":
		s.log.Warn("chat send steered into the running turn but was not recorded",
			"session", id, "error", err)
		return ports.MessageReceipt{Outcome: ports.MessageDelivered}, true, nil
	case errors.Is(err, ErrNoActiveTurn), errors.Is(err, ErrSteerUnsupported),
		errors.Is(err, ErrTurnNotSteerable), errors.Is(err, ErrSteerContentUnsupported),
		errors.Is(err, ErrProviderRefused), errors.Is(err, ErrControllerHandoff):
		s.log.Info("chat send could not be steered into the running turn; queued",
			"session", id, "error", err)
		return ports.MessageReceipt{}, false, nil
	case errors.Is(err, ErrSteerDeliveryUncertain):
		return ports.MessageReceipt{}, false, err
	case errors.Is(err, errSteerProviderCallFailed):
		// The provider call failed without a definitive answer: the guidance may
		// be with the agent, so tell the sender not to resend.
		return ports.MessageReceipt{}, false, fmt.Errorf("%w: %w", ErrSteerDeliveryUncertain, err)
	default:
		// Failed before anything reached the provider (encoding the request,
		// composing queued messages into the batch): queue it as before steering.
		s.log.Warn("chat send could not be steered (before dispatch); queued",
			"session", id, "error", err)
		return ports.MessageReceipt{}, false, nil
	}
}

// RelaySessionChatTurnReceipt is RelaySessionChatTurn that also reports whether
// the turn started immediately or was queued behind a running one.
//
// A running turn blocked on an in-chat question never ends by itself, so a
// message queued behind it would wait forever: the provider is waiting for an
// answer and the sender believes it gave one. Such a message is steered into
// the running turn instead. Only a send without a client message id takes that
// path; an idempotent retry keeps the durable queue.
func (s *Service) RelaySessionChatTurnReceipt(ctx context.Context, id domain.SessionID, text, clientMessageID string, options ports.MessageDeliveryOptions) (_ ports.MessageReceipt, err error) {
	if turn, ok, err := s.queueBehindStart(ctx, id, text, clientMessageID, options); ok {
		return queuedReceipt(turn), err
	}
	started := s.now()
	controller, release, err := s.workingController(ctx, id)
	lockWait := s.now().Sub(started)
	if err != nil {
		s.logSlowRelay(id, lockWait, 0, err)
		return ports.MessageReceipt{}, err
	}
	defer release()
	defer func() { s.logSlowRelay(id, lockWait, s.now().Sub(started)-lockWait, err) }()
	msg := s.relayMessage(ctx, text, clientMessageID, options)
	waitingOnInput := controller.WaitingOnInput()
	if waitingOnInput && clientMessageID == "" {
		steered, err := controller.Steer(ctx, msg)
		switch {
		case err == nil:
			return ports.MessageReceipt{Outcome: ports.MessageDelivered, ID: steered.ActivityID, WaitingOnInput: true}, nil
		case steered.ProviderTurnID != "":
			// The agent has the message; only Tallpine's timeline record of it failed.
			s.log.Warn("chat send steered past an open question but was not recorded",
				"session", id, "error", err)
			return ports.MessageReceipt{Outcome: ports.MessageDelivered, WaitingOnInput: true}, nil
		case errors.Is(err, ErrNoActiveTurn):
			// The question was answered and the turn ended meanwhile; send normally.
			waitingOnInput = false
		case errors.Is(err, ErrSteerUnsupported), errors.Is(err, ErrTurnNotSteerable),
			errors.Is(err, ErrSteerContentUnsupported), errors.Is(err, ErrProviderRefused):
			// The provider definitely did not take it. Queue it, and tell the
			// sender why the agent will not see it yet.
			s.log.Info("chat send to a turn waiting on input could not be steered; queued",
				"session", id, "error", err)
		default:
			return ports.MessageReceipt{}, err
		}
	}
	if !waitingOnInput && msg.ClientMessageID == "" && controller.busy() {
		receipt, ok, err := s.steerRelayIntoBusyTurn(ctx, id, controller, msg)
		if err != nil || ok {
			return receipt, err
		}
	}
	turn, err := controller.Send(ctx, msg)
	if err != nil {
		return ports.MessageReceipt{}, err
	}
	// A duplicate client message id yields an empty turn: nothing new was
	// dispatched, and the original may still be queued.
	if turn.ID == "" {
		return ports.MessageReceipt{Outcome: ports.MessageDuplicate}, nil
	}
	if turn.State == domain.TurnStateQueued {
		return ports.MessageReceipt{Outcome: ports.MessageQueued, ID: turn.ID, WaitingOnInput: waitingOnInput}, nil
	}
	return ports.MessageReceipt{Outcome: ports.MessageDelivered, ID: turn.ID}, nil
}

func (s *Service) relayChatTurnReceipt(
	ctx context.Context,
	id domain.SessionID,
	text, clientMessageID string,
	options ports.MessageDeliveryOptions,
) (domain.ConversationTurn, error) {
	if turn, ok, err := s.queueBehindStart(ctx, id, text, clientMessageID, options); ok {
		return turn, err
	}
	started := s.now()
	controller, release, err := s.workingController(ctx, id)
	lockWait := s.now().Sub(started)
	if err != nil {
		s.logSlowRelay(id, lockWait, 0, err)
		return domain.ConversationTurn{}, err
	}
	defer release()
	turn, err := controller.Send(ctx, s.relayMessage(ctx, text, clientMessageID, options))
	s.logSlowRelay(id, lockWait, s.now().Sub(started)-lockWait, err)
	if err != nil {
		return domain.ConversationTurn{}, err
	}
	return turn, nil
}

// maxReportRedispatch bounds derived-key redispatches of one report batch.
const maxReportRedispatch = 8

// SubmitReports leaves a busy target's reports in the durable outbox. Unlike
// ordinary Send, acceptance here means the turn crossed the dispatch boundary,
// not that stale prose was copied into a future chat turn.
func (s *Service) SubmitReports(ctx context.Context, id domain.SessionID, text, clientMessageID string) error {
	controller, release, err := s.workingController(ctx, id)
	if err != nil {
		return err
	}
	defer release()
	controller.sendMu.Lock()
	defer controller.sendMu.Unlock()
	// A turn that definitively never reached the provider (ErrChatTurnNotAccepted) so the batch is retried under a derived key.
	// Any other failure is ambiguous and stays unconfirmed.
	key := clientMessageID
	for attempt := 1; ; attempt++ {
		existing, found, err := controller.store.ConversationMessageByClientID(ctx, controller.conversation.ID, key)
		if err != nil {
			return err
		}
		if !found {
			break
		}
		turn, err := controller.store.TurnByID(ctx, existing.TurnID)
		if err != nil {
			return err
		}
		if turn.State == domain.TurnStateQueued {
			return reportsvc.ErrOrchestratorBusy
		}
		if turn.ProviderTurnID == "" && turn.State == domain.TurnStateFailed &&
			strings.Contains(turn.ErrorMessage, ports.ErrChatTurnNotAccepted.Error()) && attempt < maxReportRedispatch {
			key = fmt.Sprintf("%s#%d", clientMessageID, attempt)
			continue
		}
		if turn.ProviderTurnID == "" || turn.State == domain.TurnStateFailed {
			// SendTurn errors are ambiguous: the provider may have accepted the
			// prompt before the receipt was lost. Keep the outbox pending for
			// recovery/explicit retry rather than acknowledge or resend it.
			return fmt.Errorf("%w: turn %s is unconfirmed; explicit recovery: after checking the provider, send a new Chat message to include pending worker reports", reportsvc.ErrDeliveryUnconfirmed, turn.ID)
		}
		return nil // durable provider acceptance survives retry/restart
	}
	if until, paused := controller.usageLimitPausedUntil(ctx); paused {
		return &reportsvc.UsageLimitPausedError{Until: until}
	}
	if controller.busy() {
		return reportsvc.ErrOrchestratorBusy
	}
	if _, err := controller.store.NextQueuedTurn(ctx, controller.conversation.ID); err == nil {
		return reportsvc.ErrOrchestratorBusy // queued user work has priority
	} else if !errors.Is(err, domain.ErrNoQueuedTurn) {
		return err
	}
	_, err = controller.sendLocked(ctx, s.relayMessage(ctx, text, key, ports.MessageDeliveryOptions{}), false, nil, nil)
	return err
}

// relayMessage is the automation-origin message Tallpine carries for a sender.
func (s *Service) relayMessage(ctx context.Context, text, clientMessageID string, options ports.MessageDeliveryOptions) ports.ChatUserMessage {
	// A server-owned id distinguishes worker coordination from user-authored
	// relays after restart, without guessing from a prose prefix.
	if clientMessageID == "" && !options.AuthoredByUser && options.SenderSessionID != "" && s.sessions != nil {
		if sender, found, err := s.sessions.GetSession(ctx, domain.SessionID(options.SenderSessionID)); err == nil && found && sender.Kind == domain.KindWorker {
			clientMessageID = workerSendPrefix + uuid.NewString()
		}
	}
	return s.resolveSteerSender(ctx, ports.ChatUserMessage{
		Text: text, ClientMessageID: clientMessageID,
		SenderSessionID: options.SenderSessionID, InteractionAt: options.InteractionAt,
		Origin: domain.MessageOriginAutomation, AuthoredByUser: options.AuthoredByUser,
	})
}

// RelaySessionChatTurn preserves cooperative sender identity and automation origin.
func (s *Service) RelaySessionChatTurn(ctx context.Context, id domain.SessionID, text, clientMessageID string, options ports.MessageDeliveryOptions) (string, error) {
	return s.relayChatTurn(ctx, id, text, clientMessageID, options)
}

// StopChat releases a session's controller.
func (s *Service) StopChat(ctx context.Context, id domain.SessionID) error {
	return s.Stop(ctx, id)
}

// usageLimitPausedUntil reports whether the conversation's latest turn failed on
// a provider quota refusal whose reset is still ahead. It reads the durable turn,
// so a daemon restart keeps the pause.
func (c *Controller) usageLimitPausedUntil(ctx context.Context) (time.Time, bool) {
	// The latest turn that has left the queue: queued nudges are newer than the
	// failure that paused the session and must not hide it.
	reader, ok := c.store.(interface {
		LatestAutoResumeTurn(context.Context, string) (domain.ConversationTurn, error)
	})
	if !ok {
		return time.Time{}, false
	}
	turn, err := reader.LatestAutoResumeTurn(ctx, c.conversation.ID)
	if err != nil || turn.State != domain.TurnStateFailed || turn.UsageLimit == nil || turn.CompletedAt == nil {
		return time.Time{}, false
	}
	until := turn.UsageLimit.PausedUntil(*turn.CompletedAt)
	if !until.After(c.now()) {
		return time.Time{}, false
	}
	// The refusal belongs to the account that issued it; a move ends the pause.
	if mover, ok := c.store.(interface {
		SessionAccountChangedSince(context.Context, domain.SessionID, time.Time) (bool, error)
	}); ok {
		if changed, err := mover.SessionAccountChangedSince(ctx, c.sessionID, turn.RequestedAt); err == nil && changed {
			return time.Time{}, false
		}
	}
	return until, true
}
