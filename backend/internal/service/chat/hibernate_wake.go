package chat

import (
	"context"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/backoff"
	"tallpine.eu/tallpine/backend/internal/domain"
)

func (s *Service) wakeHibernated(ctx context.Context, id domain.SessionID) error {
	if s.wakeChat == nil {
		return ErrNoController
	}
	if controller, err := s.Controller(id); err == nil {
		controller.mu.Lock()
		hibernating := controller.handoff == controllerHandoffHibernate
		controller.mu.Unlock()
		if hibernating {
			waitCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()
			gate := s.controllerGate(domain.SessionConversationOwner(id))
			if err := gate.lock(waitCtx); err != nil {
				return err
			}
			select {
			case <-controller.stopped:
			default:
				current, _ := s.Controller(id)
				if current == controller && s.stopProviderHost != nil {
					// New user intent can complete a previously failed shutdown.
					// Never send to the fenced source or terminate a replacement.
					if err := s.stopProviderHost(waitCtx, id); err != nil {
						gate.unlock()
						return fmt.Errorf("finish hibernated chat shutdown: %w", err)
					}
				}
			}
			gate.unlock()
			select {
			case <-controller.stopped:
			case <-waitCtx.Done():
				return waitCtx.Err()
			}
		}
	}
	s.wakeMu.Lock()
	if run := s.wakeRuns[id]; run != nil {
		s.wakeMu.Unlock()
		select {
		case <-run.done:
			return run.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	// Read after joining any existing wake. A stale pre-lock snapshot must not
	// create another automatic attempt after a failed wake clears the marker.
	rec, err := s.requireChatSession(ctx, id)
	if err != nil {
		s.wakeMu.Unlock()
		return err
	}
	if rec.HibernatedAt == nil {
		s.wakeMu.Unlock()
		if s.HasLiveChatController(id) {
			return nil
		}
		return ErrNoController
	}
	run := &wakeRun{done: make(chan struct{})}
	s.wakeRuns[id] = run
	s.waking[id]++
	s.wakeMu.Unlock()
	finish := func(resultErr error) error {
		s.wakeMu.Lock()
		run.err = resultErr
		delete(s.wakeRuns, id)
		s.waking[id]--
		if s.waking[id] == 0 {
			delete(s.waking, id)
		}
		close(run.done)
		s.wakeMu.Unlock()
		return resultErr
	}
	wakeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
	defer cancel()
	if err := s.wakeChat(wakeCtx, id); err != nil {
		return finish(err)
	}
	if !s.HasLiveChatController(id) {
		return finish(ErrNoController)
	}
	return finish(nil)
}

// startBackgroundWake accepts a hibernated send before provider startup. The
// durable queue is the acknowledgement boundary; wake and drain happen after
// the HTTP request returns so the composer never exposes startup latency.
func (s *Service) startBackgroundWake(id domain.SessionID) {
	s.runBackgroundWake(id, false)
}

// Wake retries. A message accepted for a sleeping agent was acknowledged to
// its sender, which for a worker report or a CI reaction is another program
// that will not send it again, so a failed wake keeps it queued and tries
// again later rather than failing it at once.
const (
	chatWakeAttempts     = 5
	chatWakeRetryBackoff = 30 * time.Second
)

type wakeRetry struct {
	failures int
	next     time.Time
}

func (s *Service) runBackgroundWake(id domain.SessionID, retry bool) {
	if s.parking.Load() {
		// A daemon stopping its agents must not start one. The message stays
		// queued with the sleep marker, and the next daemon wakes the agent.
		return
	}
	s.wakeMu.Lock()
	if s.backgroundWakes[id] {
		s.wakeMu.Unlock()
		return
	}
	// RetryQueuedWakes picks due sessions before it starts them, so a wake that
	// fails in between has already scheduled its next retry: starting now would
	// be an attempt inside the backoff window that deferWake does not count.
	if pending, ok := s.wakeRetries[id]; retry && ok && pending.next.After(s.now()) {
		s.wakeMu.Unlock()
		return
	}
	s.backgroundWakes[id] = true
	s.wakeMu.Unlock()
	go func() {
		defer func() {
			s.wakeMu.Lock()
			delete(s.backgroundWakes, id)
			s.wakeMu.Unlock()
		}()
		ctx := context.Background()
		wake := s.wakeHibernated
		if retry {
			wake = s.wakeForQueuedTurns
		}
		if err := wake(ctx, id); err != nil {
			if s.stoppedForUpdate(id) {
				s.log.Warn("chat session could not wake during a harness update; queued messages kept for its resume", "session", id, "err", err)
				return
			}
			if s.versionSwitchFailed(ctx, id, err) {
				return
			}
			if s.deferWake(id) {
				s.keepAsleepForRetry(ctx, id)
				s.log.Warn("chat session could not wake; queued messages kept for a retry", "session", id, "err", err)
				return
			}
			s.giveUpWake(ctx, id, fmt.Sprintf("could not wake the agent: %v", err))
			return
		}
		s.takeVersionSwitch(id)
		if err := s.DrainQueued(ctx, id); err != nil {
			if s.deferWake(id) {
				s.keepAsleepForRetry(ctx, id)
				s.log.Warn("chat session woke but could not take its queued messages; kept for a retry", "session", id, "err", err)
				return
			}
			s.giveUpWake(ctx, id, fmt.Sprintf("could not deliver the message: %v", err))
			return
		}
		s.wakeMu.Lock()
		delete(s.wakeRetries, id)
		s.wakeMu.Unlock()
	}()
}

// deferWake records a failed wake. It reports false, and forgets the session,
// once the attempts are spent, so a person sees a lasting failure instead of
// a message that never leaves the queue.
func (s *Service) deferWake(id domain.SessionID) bool {
	s.wakeMu.Lock()
	defer s.wakeMu.Unlock()
	if s.wakeRetries == nil {
		s.wakeRetries = make(map[domain.SessionID]wakeRetry)
	}
	retry, pending := s.wakeRetries[id]
	if pending && retry.next.After(s.now()) {
		// Retries start only once due, so a failure inside the backoff window
		// is one already counted: every joiner of a shared wake (a view, a
		// send, the background drain) reports the same failure.
		return true
	}
	retry.failures++
	if retry.failures >= chatWakeAttempts {
		delete(s.wakeRetries, id)
		return false
	}
	retry.next = s.now().Add(backoff.Doubling(chatWakeRetryBackoff, chatWakeRetryBackoff<<chatWakeAttempts, retry.failures))
	s.wakeRetries[id] = retry
	return true
}

// keepAsleepForRetry records the sleep marker again for a session whose wake
// failed. A failed resume clears it, and the retry schedule lives in memory, so
// without it a daemon restart, quit or crash during the backoff would leave
// acknowledged messages queued with nothing to wake their agent. With it the
// next daemon's RetryQueuedWakes scan finds the session. Only a session with
// no live controller is marked, under the gate a start also takes.
func (s *Service) keepAsleepForRetry(ctx context.Context, id domain.SessionID) {
	marker, ok := s.sessions.(hibernationStore)
	if !ok {
		return
	}
	gate := s.controllerGate(domain.SessionConversationOwner(id))
	if err := gate.lock(ctx); err != nil {
		return
	}
	defer gate.unlock()
	if s.HasLiveChatController(id) {
		return
	}
	for range 3 {
		rec, found, err := s.sessions.GetSession(ctx, id)
		if err != nil || !found || rec.IsTerminated || rec.HibernatedAt != nil ||
			rec.Metadata.ProviderConversationID == "" {
			return
		}
		at := s.now()
		applied, err := marker.SetSessionHibernated(ctx, id, rec.Revision, &at)
		if err != nil {
			s.log.Warn("could not keep chat session asleep for its wake retry", "session", id, "err", err)
			return
		}
		if applied {
			return
		}
	}
}

// wakeForQueuedTurns starts the agent of a session whose earlier wake failed.
// A failed resume clears the sleep marker, so this restarts a stopped Chat
// agent directly, as the Resume action does, when messages wait for it.
func (s *Service) wakeForQueuedTurns(ctx context.Context, id domain.SessionID) error {
	rec, err := s.requireChatSession(ctx, id)
	if err != nil {
		return err
	}
	if rec.IsTerminated {
		return ErrNoController
	}
	if rec.HibernatedAt != nil {
		return s.wakeHibernated(ctx, id)
	}
	if s.HasLiveChatController(id) {
		return nil
	}
	if s.wakeChat == nil {
		return ErrNoController
	}
	// Count as waking, so the start keeps the queued messages for the drain
	// that follows instead of treating them as stale intake.
	s.wakeMu.Lock()
	s.waking[id]++
	s.wakeMu.Unlock()
	defer func() {
		s.wakeMu.Lock()
		s.waking[id]--
		if s.waking[id] == 0 {
			delete(s.waking, id)
		}
		s.wakeMu.Unlock()
	}()
	wakeCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := s.wakeChat(wakeCtx, id); err != nil {
		return err
	}
	if !s.HasLiveChatController(id) {
		return ErrNoController
	}
	return nil
}

type hibernatedQueueLister interface {
	ListHibernatedChatsWithQueuedTurns(context.Context) ([]domain.SessionID, error)
}

// RetryQueuedWakes wakes sleeping sessions that hold queued messages: a
// daemon that stopped between accepting a message and waking its agent, or a
// wake that failed and is due to be tried again.
func (s *Service) RetryQueuedWakes(ctx context.Context) error {
	if s.parking.Load() {
		return nil
	}
	now := s.now()
	due := make(map[domain.SessionID]bool)
	s.wakeMu.Lock()
	for id, retry := range s.wakeRetries {
		if !retry.next.After(now) {
			due[id] = true
		}
	}
	s.wakeMu.Unlock()
	var listErr error
	if lister, ok := s.sessions.(hibernatedQueueLister); ok {
		ids, err := lister.ListHibernatedChatsWithQueuedTurns(ctx)
		if err != nil {
			listErr = err
		}
		s.wakeMu.Lock()
		for _, id := range ids {
			if retry, waiting := s.wakeRetries[id]; !waiting || !retry.next.After(now) {
				due[id] = true
			}
		}
		s.wakeMu.Unlock()
	}
	for id := range due {
		conversation, err := s.store.ConversationForSession(ctx, id)
		if err != nil {
			continue
		}
		if _, err := s.store.NextQueuedTurn(ctx, conversation.ID); err != nil {
			if errors.Is(err, domain.ErrNoQueuedTurn) && s.hasChatView(id) {
				// A viewed session whose wake failed retries without a message:
				// the person is looking at it and expects it to come back.
				s.runBackgroundWake(id, true)
				continue
			}
			if errors.Is(err, domain.ErrNoQueuedTurn) {
				s.wakeMu.Lock()
				delete(s.wakeRetries, id)
				s.wakeMu.Unlock()
			}
			continue
		}
		s.runBackgroundWake(id, true)
	}
	return listErr
}

func (s *Service) failQueuedTurns(ctx context.Context, id domain.SessionID, message string) {
	conversation, err := s.store.ConversationForSession(ctx, id)
	if err != nil {
		return
	}
	for {
		queued, err := s.store.NextQueuedTurn(ctx, conversation.ID)
		if errors.Is(err, domain.ErrNoQueuedTurn) {
			return
		}
		if err != nil {
			return
		}
		if err := s.store.SettleTurnByID(ctx, queued.TurnID, domain.TurnStateFailed, message, s.now()); err != nil {
			return
		}
	}
}

// isWaking also covers a session stopped for an update, so the start that
// resumes it keeps the messages queued meanwhile, as a wake does. Once a
// provider runs again, the update's stop is over.
func (s *Service) isWaking(id domain.SessionID) bool {
	live := s.HasLiveChatController(id)
	s.wakeMu.Lock()
	defer s.wakeMu.Unlock()
	if live {
		delete(s.updateStops, id)
	}
	return s.waking[id] > 0 || s.updateStops[id]
}

// stoppedForUpdate reports whether an update stopped id and nothing has
// started it again since.
func (s *Service) stoppedForUpdate(id domain.SessionID) bool {
	s.wakeMu.Lock()
	defer s.wakeMu.Unlock()
	return s.updateStops[id]
}
