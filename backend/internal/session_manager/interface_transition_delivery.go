package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func (m *Manager) deliverTransitionMessages(
	ctx context.Context,
	transition domain.SessionInterfaceTransition,
) error {
	m.transitionDeliveryAttemptMu.Lock()
	defer m.transitionDeliveryAttemptMu.Unlock()
	store, ok := m.store.(interfaceTransitionStore)
	if !ok {
		return ports.ErrChatUnsupported
	}
	ctx, cancel := context.WithTimeout(ctx, interfaceTransitionStepLimit)
	defer cancel()
	messages, err := store.ListPendingSessionInterfaceTransitionMessages(ctx, transition.ID)
	if err != nil {
		return fmt.Errorf("read transition %s messages: %w", transition.ID, err)
	}
	if len(messages) == 0 {
		return nil
	}
	rec, err := m.waitForTransitionDeliveryReady(ctx, transition.SessionID, time.Time{})
	if err != nil {
		return fmt.Errorf("wait to deliver transition %s messages: %w", transition.ID, err)
	}
	lastTerminalInputAt, releaseTerminalInput := m.beginTerminalInputDrain(rec)
	if releaseTerminalInput != nil {
		defer releaseTerminalInput()
		// Closing the raw-input gate and rechecking readiness makes queued Tallpine
		// delivery the next instruction accepted by the TUI controller.
		if _, err := m.waitForTransitionDeliveryReady(ctx, transition.SessionID, lastTerminalInputAt); err != nil {
			return fmt.Errorf("recheck transition %s delivery readiness: %w", transition.ID, err)
		}
	}
	// These sends pass the unsent-draft check like any other. The input gate
	// held above means no keystroke can land meanwhile, and a refusal returns an
	// error that leaves the message pending in this durable outbox for the next
	// delivery attempt, so nothing is lost or concatenated.
	for _, message := range messages {
		if err := m.send(ctx, transition.SessionID, message.Message, message.ClientMessageID, ports.MessageDeliveryOptions{AuthoredByUser: message.AuthoredByUser, SenderSessionID: message.SenderSessionID, InteractionAt: message.CreatedAt}); err != nil {
			return fmt.Errorf("deliver transition %s message %d: %w", transition.ID, message.ID, err)
		}
		if err := store.MarkSessionInterfaceTransitionMessageDelivered(ctx, message.ID, m.clock()); err != nil {
			return fmt.Errorf("acknowledge transition %s message %d: %w", transition.ID, message.ID, err)
		}
	}
	return nil
}

func (m *Manager) waitForTransitionDeliveryReady(
	ctx context.Context,
	id domain.SessionID,
	lastTerminalInputAt time.Time,
) (domain.SessionRecord, error) {
	ticker := time.NewTicker(interfaceTransitionPoll)
	defer ticker.Stop()
	idleSince := time.Time{}
	for {
		rec, ok, err := m.store.GetSession(ctx, id)
		if err != nil {
			return domain.SessionRecord{}, err
		}
		if !ok {
			return domain.SessionRecord{}, ErrNotFound
		}
		if rec.IsTerminated || rec.Activity.State == domain.ActivityExited {
			return domain.SessionRecord{}, ErrTerminated
		}
		ready := rec.Activity.State == domain.ActivityIdle
		if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeTUI {
			// MarkSpawned intentionally clears FirstSignalAt. Waiting for a signal
			// prevents replaying a queued prompt into the TUI before its native
			// initialization/resume sequence has reached an input-ready state.
			ready = ready && !rec.FirstSignalAt.IsZero() && tuiIdleAfterInput(rec, lastTerminalInputAt)
		}
		if ready {
			if idleSince.IsZero() {
				idleSince = time.Now()
			} else if time.Since(idleSince) >= interfaceTransitionIdleSettle {
				return rec, nil
			}
		} else {
			idleSince = time.Time{}
		}
		select {
		case <-ctx.Done():
			return domain.SessionRecord{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (m *Manager) queueDuringInterfaceTransition(
	ctx context.Context,
	id domain.SessionID,
	message, clientMessageID string,
	options ports.MessageDeliveryOptions,
) (string, error) {
	store, ok := m.store.(interfaceTransitionStore)
	if !ok {
		return "", nil
	}
	transition, active, err := store.GetActiveSessionInterfaceTransition(ctx, id)
	if err != nil || !active {
		return "", err
	}
	if strings.TrimSpace(clientMessageID) == "" {
		clientMessageID = "interface-transition:" + m.newLaunchID()
	}
	at := options.InteractionAt
	if at.IsZero() {
		at = m.clock()
	}
	if err := store.EnqueueSessionInterfaceTransitionMessage(
		ctx, transition.ID, clientMessageID, message, at, options,
	); err != nil {
		return "", err
	}
	return clientMessageID, nil
}

// settleTransitionMessages runs after every transition outcome, not only the
// happy path. The immediate attempt makes rollback/cancellation responsive;
// the daemon-lifetime worker retains responsibility after any transient error.
func (m *Manager) settleTransitionMessages(transition domain.SessionInterfaceTransition) {
	store, ok := m.store.(interfaceTransitionStore)
	if !ok {
		m.logger.Error("interface transition: store does not support queued messages", "transitionID", transition.ID)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), interfaceTransitionStepLimit)
	current, ok, err := store.GetSessionInterfaceTransition(ctx, transition.ID)
	cancel()
	if err != nil {
		m.logger.Error("interface transition: read terminal state for queued messages",
			"transitionID", transition.ID, "error", err)
		m.wakeTransitionMessageDispatcher()
		return
	}
	if !ok || !current.Phase.Terminal() {
		m.wakeTransitionMessageDispatcher()
		return
	}
	if err := m.deliverTransitionMessages(context.Background(), current); err != nil {
		m.logger.Error("interface transition: queued-message delivery deferred",
			"transitionID", transition.ID, "error", err)
	}
	m.wakeTransitionMessageDispatcher()
}

func (m *Manager) deliverAllTransitionMessages(ctx context.Context) error {
	store, ok := m.store.(interfaceTransitionStore)
	if !ok {
		return nil
	}
	transitions, err := store.ListDeliverableSessionInterfaceTransitions(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, transition := range transitions {
		if err := m.deliverTransitionMessages(ctx, transition); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (m *Manager) wakeTransitionMessageDispatcher() {
	select {
	case m.transitionDeliveryWake <- struct{}{}:
	default:
	}
}

// startTransitionMessageDispatcher starts one daemon-lifetime durable outbox
// worker. Reconcile supplies the daemon context, so shutdown bounds the worker;
// a periodic scan repairs crashes that happened after a transition became
// terminal but before its in-memory wake-up.
func (m *Manager) startTransitionMessageDispatcher(ctx context.Context) {
	if _, ok := m.store.(interfaceTransitionStore); !ok {
		return
	}
	m.transitionDeliveryMu.Lock()
	if m.transitionDeliveryRunning {
		m.transitionDeliveryMu.Unlock()
		return
	}
	m.transitionDeliveryRunning = true
	m.transitionDeliveryMu.Unlock()

	go func() {
		defer func() {
			m.transitionDeliveryMu.Lock()
			m.transitionDeliveryRunning = false
			m.transitionDeliveryMu.Unlock()
		}()
		delay := time.Duration(0)
		for {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return
			case <-m.transitionDeliveryWake:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
			case <-timer.C:
			}
			if err := m.deliverAllTransitionMessages(ctx); err != nil {
				m.logger.Error("interface transition: retry queued-message delivery", "error", err)
				delay = interfaceDeliveryRetry
			} else {
				delay = interfaceDeliveryIdlePoll
			}
		}
	}()
}
