package chat

import (
	"context"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// HibernateChat stops a quiescent provider without ending its Tallpine session. A
// false result means the final locked eligibility check found useful work.
func (s *Service) HibernateChat(ctx context.Context, id domain.SessionID) (bool, error) {
	return s.hibernateChat(ctx, id, hibernateOptions{minIdle: chatHibernateGrace})
}

// HibernateChatForVersionRefresh stops an idle provider that still runs a
// harness release an update replaced, whatever the hibernation setting. It
// applies the same checks as idle hibernation, so it never stops a provider
// with a turn, queued message, pending approval or background work, and never
// one the user is viewing. minIdle keeps a recently used session, whose
// prompt cache is still warm, on its release until it has been quiet longer.
func (s *Service) HibernateChatForVersionRefresh(ctx context.Context, id domain.SessionID, minIdle time.Duration) (bool, error) {
	return s.hibernateChat(ctx, id, hibernateOptions{versionRefresh: true, minIdle: max(minIdle, chatHibernateGrace)})
}

// HibernateChatForUpdate stops an idle provider, whatever the hibernation
// setting and even while the user views it, so an installer that cannot
// replace a running executable can run. Work in flight, queued messages,
// pending approvals and background work still keep it running.
func (s *Service) HibernateChatForUpdate(ctx context.Context, id domain.SessionID) (bool, error) {
	return s.hibernateChat(ctx, id, hibernateOptions{forUpdate: true, ignoreViews: true})
}

func (s *Service) hibernateChat(ctx context.Context, id domain.SessionID, opts hibernateOptions) (bool, error) {
	// Skip eligibility and queue reads while the feature is off. Recheck before
	// stopping the provider in case the setting changes during those reads.
	if !opts.enabled(s) {
		return false, nil
	}
	marker, ok := s.sessions.(hibernationStore)
	if !ok {
		return false, errors.New("chat hibernation store is unavailable")
	}
	turns, ok := s.sessions.(hibernationTurnReader)
	if !ok {
		return false, errors.New("chat hibernation turn reader is unavailable")
	}
	owner := domain.SessionConversationOwner(id)
	gate := s.controllerGate(owner)
	if !gate.tryLock() {
		return false, nil
	}
	rec, err := s.requireChatSession(ctx, id)
	if err != nil {
		gate.unlock()
		return false, err
	}
	if !opts.eligible(ctx, s, rec) || opts.viewed(s, id) {
		gate.unlock()
		return false, nil
	}
	controller, err := s.Controller(id)
	if err != nil || controller.State() != ports.ChatControllerReady ||
		!controller.Capabilities().Has(ports.ChatCapabilityResume) {
		gate.unlock()
		return false, nil
	}
	hibernator, ok := controller.conv.(ports.ChatProviderHibernator)
	if !ok {
		gate.unlock()
		return false, nil
	}
	// Inventory is provider I/O. Keep hibernation out with an operation count,
	// while allowing new views, sends and explicit Kill to proceed.
	release := controller.beginOperation()
	gate.unlock()
	ready, err := s.hibernationReady(ctx, id, hibernator)
	release()
	if err != nil || !ready {
		if err != nil {
			return false, fmt.Errorf("check chat provider background work: %w", err)
		}
		return false, nil
	}
	if !gate.tryLock() {
		return false, nil
	}
	defer gate.unlock()
	current, err := s.Controller(id)
	if err != nil || current != controller || opts.viewed(s, id) {
		return false, nil
	}
	// Send and provider lifecycle projection use the same dispatch lock. Fence
	// intake only after verifying the durable queue and latest primary turn.
	if !controller.sendMu.TryLock() {
		return false, nil
	}
	fresh, ok, err := s.fencedHibernationChecks(ctx, id, controller, turns, opts)
	if err != nil || !ok {
		return false, err
	}
	// Persist the intent while intake is fenced. A failed or contested write
	// leaves the provider alive; a crash during shutdown can finish it on boot.
	at := s.now()
	applied, err := marker.SetSessionHibernated(ctx, id, fresh.Revision, &at)
	if err != nil || !applied {
		controller.sendMu.Unlock()
		return false, err
	}
	controller.mu.Lock()
	controller.handoff = controllerHandoffHibernate
	controller.suppressStoppedActivity = true
	controller.mu.Unlock()
	controller.sendMu.Unlock()

	if err := s.awaitHibernatedStop(ctx, id, controller, hibernator); err != nil {
		return false, err
	}
	if opts.versionRefresh {
		// The wake that follows starts the installed release; remember which
		// one, so a failed start keeps the user's queued messages.
		s.noteVersionSwitch(id, fresh.StaleBinary.Version)
		s.log.Info("chat session stopped to move to a new harness release", "session", id, "harness", fresh.Harness, "version", fresh.StaleBinary.Version)
		return true, nil
	}
	if opts.forUpdate {
		// The update starts it again once installed. Until then a wake can
		// time out on the installer, so what queues meanwhile must not fail.
		s.wakeMu.Lock()
		if s.updateStops == nil {
			s.updateStops = make(map[domain.SessionID]bool)
		}
		s.updateStops[id] = true
		s.wakeMu.Unlock()
		s.log.Info("chat session stopped for a harness update", "session", id, "harness", fresh.Harness)
		return true, nil
	}
	s.log.Info("chat session hibernated", "session", id, "harness", fresh.Harness)
	return true, nil
}

// Explicit controller teardown (kill or interface switch) consumes the cold
// marker so a later Chat controller cannot inherit a stale sleep state.
func (s *Service) clearHibernation(ctx context.Context, id domain.SessionID) error {
	s.wakeMu.Lock()
	delete(s.updateStops, id)
	delete(s.wakeRetries, id)
	s.wakeMu.Unlock()
	if s.sessions == nil {
		return nil
	}
	marker, ok := s.sessions.(hibernationStore)
	for range 3 {
		rec, found, err := s.sessions.GetSession(ctx, id)
		if err != nil || !found || rec.HibernatedAt == nil {
			return err
		}
		if !ok {
			return errors.New("chat hibernation store is unavailable")
		}
		cleared, err := marker.SetSessionHibernated(ctx, id, rec.Revision, nil)
		if err != nil {
			return err
		}
		if cleared {
			return nil
		}
	}
	return errors.New("chat hibernation marker changed concurrently")
}

// hibernationReady asks the provider whether it can hibernate and vetoes it
// while the agent has background processes.
func (s *Service) hibernationReady(ctx context.Context, id domain.SessionID, hibernator ports.ChatProviderHibernator) (bool, error) {
	ready, err := hibernator.CanHibernate(ctx)
	if err == nil && ready && s.backgroundWork != nil {
		// The provider's own registry misses work it does not track: a shell
		// a tool call left running, a watch loop, a build started with &.
		// Stopping the agent's process group would kill those, so any such
		// process keeps the agent awake.
		busy, probeErr := s.backgroundWork(ctx, id)
		switch {
		case probeErr != nil:
			// Unreadable counts as busy; the next sweep looks again.
			ready = false
			s.log.Debug("chat hibernation skipped: agent processes unreadable", "session", id, "err", probeErr)
		case busy:
			ready = false
			s.log.Debug("chat hibernation skipped: agent has background processes", "session", id)
		}
	}
	return ready, err
}

// fencedHibernationChecks re-verifies, with intake fenced (sendMu held),
// that the chat is quiet. On ok=true the sendMu stays held for the caller.
func (s *Service) fencedHibernationChecks(ctx context.Context, id domain.SessionID, controller *Controller, turns hibernationTurnReader, opts hibernateOptions) (fresh domain.SessionRecord, ok bool, err error) {
	controller.mu.Lock()
	busy := controller.busyLocked()
	controller.mu.Unlock()
	if busy {
		controller.sendMu.Unlock()
		return domain.SessionRecord{}, false, nil
	}
	if _, err := s.store.NextQueuedTurn(ctx, controller.conversation.ID); err == nil {
		controller.sendMu.Unlock()
		return domain.SessionRecord{}, false, nil
	} else if !errors.Is(err, domain.ErrNoQueuedTurn) {
		controller.sendMu.Unlock()
		return domain.SessionRecord{}, false, fmt.Errorf("check queued chat turns: %w", err)
	}
	if running, err := s.store.ListVisibleRunningTurnProviderIDs(ctx, controller.conversation.ID); err != nil {
		controller.sendMu.Unlock()
		return domain.SessionRecord{}, false, fmt.Errorf("check running chat turns: %w", err)
	} else if len(running) != 0 {
		controller.sendMu.Unlock()
		return domain.SessionRecord{}, false, nil
	}
	if pending, err := s.store.HasPendingConversationInteractions(ctx, controller.conversation.ID); err != nil {
		controller.sendMu.Unlock()
		return domain.SessionRecord{}, false, fmt.Errorf("check pending chat interactions: %w", err)
	} else if pending {
		controller.sendMu.Unlock()
		return domain.SessionRecord{}, false, nil
	}
	settled, err := turns.LatestVisibleUserTurnSettled(ctx, controller.conversation.ID, id)
	if err != nil {
		controller.sendMu.Unlock()
		return domain.SessionRecord{}, false, fmt.Errorf("check latest chat turn: %w", err)
	}
	if !settled {
		controller.sendMu.Unlock()
		return domain.SessionRecord{}, false, nil
	}
	// Activity from outside Chat can change while eligibility is read. Recheck
	// before recording the shutdown intent with a revision-checked write.
	fresh, err = s.requireChatSession(ctx, id)
	if err != nil {
		controller.sendMu.Unlock()
		return domain.SessionRecord{}, false, err
	}
	if !opts.eligible(ctx, s, fresh) {
		controller.sendMu.Unlock()
		return domain.SessionRecord{}, false, nil
	}
	if !opts.enabled(s) {
		controller.sendMu.Unlock()
		return domain.SessionRecord{}, false, nil
	}
	return fresh, true, nil
}

// awaitHibernatedStop stops the provider and waits for its controller to finish.
func (s *Service) awaitHibernatedStop(ctx context.Context, id domain.SessionID, controller *Controller, hibernator ports.ChatProviderHibernator) error {
	stopErr := hibernator.Hibernate()
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if stopErr == nil {
		select {
		case <-controller.stopped:
		case <-finishCtx.Done():
			stopErr = finishCtx.Err()
		}
	}
	if stopErr != nil {
		// The adapter may have consumed its close-once guard or already sent
		// shutdown. Complete the same owner's shutdown, preserving queued work
		// and resume intent instead of reopening intake on a dying provider.
		if s.stopProviderHost == nil {
			return fmt.Errorf("hibernate chat provider: %w", stopErr)
		}
		recoveryCtx, recoveryCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer recoveryCancel()
		if err := s.stopProviderHost(recoveryCtx, id); err != nil {
			return fmt.Errorf("confirm hibernated chat shutdown: %w", errors.Join(stopErr, err))
		}
		select {
		case <-controller.stopped:
		case <-recoveryCtx.Done():
			return fmt.Errorf("wait for hibernated chat controller: %w", recoveryCtx.Err())
		}
	}
	return nil
}
