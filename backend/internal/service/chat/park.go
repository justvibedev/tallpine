package chat

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// How long a deliberate quit lets a running turn settle after interrupting it,
// and how long it waits for one provider's process tree to end.
const (
	parkInterruptSettle = 3 * time.Second
	parkStopWait        = 10 * time.Second
)

// ParkAll stops every session's Chat agent before a deliberate quit: the app
// quitting, the OS logging out or restarting, or `tallpine stop --stop-agents`.
//
// StopAll, the restart path, only detaches: provider hosts outlive the daemon
// so a replacement adopts them with their turns intact. ParkAll instead ends
// each agent's whole process tree, so no claude, node or MCP process is left
// running without a daemon. Each session is marked asleep first, as idle
// hibernation does, so its next message, report, review reaction or view
// resumes the native conversation. A turn still running is interrupted and
// given a moment to settle; one that does not is closed out as interrupted.
// Nothing can wake an agent once parking has begun.
func (s *Service) ParkAll(ctx context.Context) {
	s.parking.Store(true)
	s.mu.RLock()
	targets := make(map[domain.SessionID]*Controller)
	for owner, controller := range s.ownerControllers {
		if owner.Kind == domain.ConversationOwnerSession {
			targets[controller.sessionID] = controller
		}
	}
	s.mu.RUnlock()
	var wg sync.WaitGroup
	for id, controller := range targets {
		wg.Go(func() {
			if err := s.parkSession(ctx, id, controller); err != nil {
				s.log.Warn("could not stop chat agent cleanly on quit", "session", id, "err", err)
			}
		})
	}
	wg.Wait()
}

// Parking reports whether a deliberate quit has begun stopping agents.
func (s *Service) Parking() bool { return s.parking.Load() }

func (s *Service) parkSession(ctx context.Context, id domain.SessionID, controller *Controller) error {
	if controller.State() == ports.ChatControllerStopped {
		return nil
	}
	if controller.notQuiescent() {
		controller.parkCut.Store(true)
		interruptCtx, cancel := context.WithTimeout(ctx, parkInterruptSettle)
		if err := controller.Interrupt(interruptCtx); err != nil && !errors.Is(err, ErrNoActiveTurn) {
			s.log.Debug("interrupt before quit failed", "session", id, "err", err)
		}
		for controller.notQuiescent() && interruptCtx.Err() == nil {
			select {
			case <-interruptCtx.Done():
			case <-time.After(50 * time.Millisecond):
			}
		}
		cancel()
	}
	gate := s.controllerGate(domain.SessionConversationOwner(id))
	if err := gate.lock(ctx); err != nil {
		return s.forceStopHost(ctx, id, fmt.Errorf("lock controller: %w", err))
	}
	defer gate.unlock()
	if current, err := s.Controller(id); err != nil || current != controller {
		return nil
	}
	s.markParked(ctx, id)
	// Fence intake as hibernation does. A dispatch stuck in its provider call
	// cannot hold the quit up; the process is stopped below either way.
	locked := false
	deadline := time.Now().Add(parkInterruptSettle)
	for !locked && time.Now().Before(deadline) {
		if locked = controller.sendMu.TryLock(); !locked {
			time.Sleep(20 * time.Millisecond)
		}
	}
	controller.mu.Lock()
	quiet := !controller.busyLocked()
	if quiet {
		// Keeps queued messages for the controller that wakes the session.
		controller.handoff = controllerHandoffHibernate
	}
	controller.suppressStoppedActivity = true
	controller.mu.Unlock()
	if locked {
		controller.sendMu.Unlock()
	}

	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), parkStopWait)
	defer cancel()
	var stopErr error
	if hibernator, ok := controller.conv.(ports.ChatProviderHibernator); ok && quiet {
		stopErr = hibernator.Hibernate()
		if stopErr == nil {
			select {
			case <-controller.stopped:
			case <-stopCtx.Done():
				stopErr = stopCtx.Err()
			}
		}
	} else {
		// A turn that would not settle is closed out by the controller's own
		// stop path, which records it as interrupted rather than running.
		stopErr = controller.Terminate(stopCtx)
	}
	if stopErr != nil {
		return s.forceStopHost(ctx, id, stopErr)
	}
	s.log.Info("chat agent stopped for quit", "session", id, "settled", quiet)
	return nil
}

// forceStopHost ends a provider host directly when its controller could not.
func (s *Service) forceStopHost(ctx context.Context, id domain.SessionID, cause error) error {
	if s.stopProviderHost == nil {
		return cause
	}
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), parkStopWait)
	defer cancel()
	if err := s.stopProviderHost(stopCtx, id); err != nil {
		return errors.Join(cause, err)
	}
	return nil
}

// markParked records the sleep marker so the session resumes on demand. A
// session with no native conversation yet has nothing to resume and starts
// fresh on its next message, so it is left unmarked.
func (s *Service) markParked(ctx context.Context, id domain.SessionID) {
	marker, ok := s.sessions.(hibernationStore)
	if !ok {
		return
	}
	for range 3 {
		rec, found, err := s.sessions.GetSession(ctx, id)
		if err != nil || !found || rec.IsTerminated || rec.HibernatedAt != nil ||
			rec.ProvisionState.WithDefault() != domain.SessionProvisionReady ||
			rec.Metadata.ProviderConversationID == "" {
			return
		}
		at := s.now()
		applied, err := marker.SetSessionHibernated(ctx, id, rec.Revision, &at)
		if err != nil {
			s.log.Warn("could not mark chat session asleep on quit", "session", id, "err", err)
			return
		}
		if applied {
			return
		}
	}
}

// notQuiescent reports whether the controller is running, dispatching or
// handing off work, which keeps it from sleeping.
func (c *Controller) notQuiescent() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.busyLocked()
}

// busyLocked is notQuiescent for callers that hold c.mu.
func (c *Controller) busyLocked() bool {
	return c.state != ports.ChatControllerReady ||
		c.handoff != controllerHandoffNone ||
		c.pendingTurnID != "" || c.dispatchingTurnID != "" ||
		c.compactionPending || c.operations != 0
}
