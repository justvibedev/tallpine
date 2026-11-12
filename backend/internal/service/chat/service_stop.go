package chat

import (
	"context"
	"errors"
	"slices"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// Stop closes a session's controller. Safe to call for a session that has none.
func (s *Service) Stop(ctx context.Context, id domain.SessionID) error {
	owner := domain.SessionConversationOwner(id)
	gate := s.controllerGate(owner)
	if err := gate.lock(ctx); err != nil {
		return err
	}
	defer gate.unlock()
	// Each side is marked closed before its host stops, and a host that fails to
	// stop stays queued for the periodic cleanup retry. Stopping the main chat
	// (Exit agent, agent switch, spawn rollback) must not depend on it.
	if err := s.closeSessionSides(ctx, id); err != nil {
		s.log.Warn("side chat cleanup deferred while stopping session", "session", id, "error", err)
	}
	// A stopped chat is not restarted for a pending research mode (B638).
	s.researchCarried.take(id)
	s.viewMu.Lock()
	delete(s.viewClosedAt, id)
	delete(s.viewLeases, id)
	s.viewMu.Unlock()

	s.mu.RLock()
	controller, ok := s.controllers[id]
	s.mu.RUnlock()
	if !ok {
		s.mu.Lock()
		delete(s.startConfigs, owner)
		delete(s.awakeInterrupted, owner)
		s.mu.Unlock()
		var stopErr error
		if s.stopProviderHost != nil {
			stopErr = s.stopProviderHost(ctx, id)
		}
		return errors.Join(stopErr, s.clearHibernation(ctx, id))
	}
	controller.mu.Lock()
	preserved := controller.preserveProviderOnStop
	hibernating := controller.handoff == controllerHandoffHibernate
	controller.mu.Unlock()
	var hostErr error
	if hibernating && s.stopProviderHost != nil {
		// Hibernate may already have consumed the adapter's close-once guard.
		// Explicit Kill can still retry authenticated shutdown of this owner.
		hostErr = s.stopProviderHost(ctx, id)
	}
	err := errors.Join(hostErr, controller.Terminate(ctx))
	if preserved && !hibernating && s.stopProviderHost != nil {
		// Close may already have retired this handle (for example after a failed
		// projection). Explicit Stop targets current session ownership under the
		// start/stop gate; a stale handle's Terminate must not target a replacement.
		err = errors.Join(err, s.stopProviderHost(ctx, id))
	}

	// Keep the only handle to a controller whose event stream has not ended. A
	// caller can then retry the idempotent close rather than assuming a timeout
	// killed it and launching a second writer. Once stopped, remove only this
	// generation so a concurrent replacement can never be deleted accidentally.
	select {
	case <-controller.stopped:
		if hibernating {
			// Explicit Kill consumes the queue that idle shutdown preserved.
			err = errors.Join(err, controller.cleanupOwnedControllerWork(ctx, s.now()))
		}
		s.mu.Lock()
		if current, found := s.controllers[id]; found && current == controller {
			delete(s.controllers, id)
		}
		delete(s.startConfigs, owner)
		delete(s.awakeInterrupted, owner)
		s.mu.Unlock()
	default:
	}
	return errors.Join(err, s.clearHibernation(ctx, id))
}

// StopForOwner closes a typed-owner controller without touching its parent
// worker's Chat controller.
func (s *Service) StopForOwner(ctx context.Context, owner domain.ConversationOwner) error {
	if owner.Kind == domain.ConversationOwnerSession {
		return s.Stop(ctx, domain.SessionID(owner.ID))
	}
	gate := s.controllerGate(owner)
	if err := gate.lock(ctx); err != nil {
		return err
	}
	defer gate.unlock()

	controller, err := s.ControllerForOwner(owner)
	if errors.Is(err, ErrNoController) {
		s.mu.Lock()
		delete(s.startConfigs, owner)
		delete(s.awakeInterrupted, owner)
		s.mu.Unlock()
		return nil
	}
	if err != nil {
		return err
	}
	err = controller.Terminate(ctx)
	select {
	case <-controller.stopped:
		s.mu.Lock()
		if s.ownerControllers[owner] == controller {
			delete(s.ownerControllers, owner)
			delete(s.startConfigs, owner)
			delete(s.awakeInterrupted, owner)
			if owner.Kind == domain.ConversationOwnerSession {
				delete(s.controllers, controller.sessionID)
			}
		}
		s.mu.Unlock()
	default:
	}
	return err
}

// StopAll closes every controller, for daemon shutdown.
func (s *Service) StopAll(ctx context.Context) {
	if s.sides != nil {
		s.sides.stopAll()
	}
	s.mu.Lock()
	type shutdownTarget struct {
		owner      domain.ConversationOwner
		id         domain.SessionID
		controller *Controller
	}
	targets := make([]shutdownTarget, 0, len(s.ownerControllers))
	for owner, controller := range s.ownerControllers {
		targets = append(targets, shutdownTarget{owner: owner, id: controller.sessionID, controller: controller})
	}
	s.mu.Unlock()
	slices.SortFunc(targets, func(a, b shutdownTarget) int {
		return strings.Compare(string(a.owner.Kind)+":"+a.owner.ID, string(b.owner.Kind)+":"+b.owner.ID)
	})

	for _, target := range targets {
		gate := s.controllerGate(target.owner)
		// Take an uncontended gate immediately so an expired shared shutdown
		// context cannot skip Close. If Start/Stop/edit/branch already holds it,
		// wait only until the original deadline — never past ShutdownTimeout.
		if !gate.tryLock() {
			if err := gate.lock(ctx); err != nil {
				s.log.Error("failed to lock chat controller gate during shutdown", "session", target.id, "error", err)
				continue
			}
		}
		s.mu.RLock()
		current, ok := s.ownerControllers[target.owner]
		s.mu.RUnlock()
		if !ok || current != target.controller {
			gate.unlock()
			continue
		}
		if err := target.controller.Close(ctx); err != nil {
			s.log.Error("failed to close chat controller", "session", target.id, "error", err)
		}
		select {
		case <-target.controller.stopped:
			s.mu.Lock()
			if current, ok := s.ownerControllers[target.owner]; ok && current == target.controller {
				delete(s.ownerControllers, target.owner)
				delete(s.startConfigs, target.owner)
				if target.owner.Kind == domain.ConversationOwnerSession {
					delete(s.controllers, target.id)
				}
			}
			s.mu.Unlock()
		default:
		}
		gate.unlock()
	}
}
