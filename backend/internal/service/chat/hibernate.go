package chat

import (
	"context"
	"errors"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// View leases survive a missed renderer heartbeat but expire after a crash.
const chatViewLease = 30 * time.Second
const chatHibernateGrace = 30 * time.Second

// chatViewWakeWait bounds how long opening a view waits for the wake it
// starts. A cold provider start can take up to its handshake timeout; a phone
// client gives up on a request after about 12 s, so the view returns and the
// wake continues, its result arriving as session events.
var chatViewWakeWait = 5 * time.Second

type wakeRun struct {
	done chan struct{}
	err  error
}

type hibernationStore interface {
	SetSessionHibernated(context.Context, domain.SessionID, int64, *time.Time) (bool, error)
}

type hibernationTurnReader interface {
	LatestVisibleUserTurnSettled(context.Context, string, domain.SessionID) (bool, error)
}

// SetWakeCallback connects a cold Chat session to Session Manager's native
// resume path. It is installed after both services have been constructed.
func (s *Service) SetWakeCallback(wake func(context.Context, domain.SessionID) error) {
	s.wakeChat = wake
}

// SetChatView records a short-lived view lease. Registration and the final
// hibernation check use the same controller gate, so opening a view either
// prevents shutdown or waits for shutdown and then wakes the native session.
func (s *Service) SetChatView(ctx context.Context, id domain.SessionID, viewID string, active bool) error {
	gate := s.controllerGate(domain.SessionConversationOwner(id))
	if !active || !gate.tryLock() {
		if active && s.isWaking(id) {
			// A wake holds the gate for its whole provider start, and a waking
			// session cannot hibernate, so the lease needs no gate. Waiting here
			// would block the viewer until the start finished or timed out.
			s.setViewLease(id, viewID, true)
			return nil
		}
		if err := gate.lock(ctx); err != nil {
			return err
		}
	}
	if !active {
		s.setViewLease(id, viewID, false)
		gate.unlock()
		return nil
	}
	rec, err := s.requireChatSession(ctx, id)
	if err != nil {
		gate.unlock()
		return err
	}
	if rec.IsTerminated {
		s.viewMu.Lock()
		delete(s.viewLeases, id)
		s.viewMu.Unlock()
		gate.unlock()
		return nil
	}
	newView := s.setViewLease(id, viewID, true)
	gate.unlock()
	// Renewing a lease keeps the view open; only a newly opened view wakes a
	// sleeping provider. A failed resume must not spawn another process on
	// every heartbeat. Explicit sends and newly opened views can still retry.
	if !newView || !s.hasChatView(id) {
		return nil
	}
	if rec.ProvisionState.WithDefault() != domain.SessionProvisionReady ||
		rec.HibernatedAt == nil || rec.Metadata.ProviderConversationID == "" {
		return nil
	}
	done := make(chan error, 1)
	go func() {
		err := s.wakeHibernated(context.WithoutCancel(ctx), id)
		if err != nil && !errors.Is(err, ErrNoController) {
			// A provider can fail its first start after a restart and succeed
			// moments later. Keep the session asleep and retry it on the wake
			// schedule while it is viewed, instead of stranding it as stopped.
			s.scheduleWakeRetry(context.WithoutCancel(ctx), id)
			s.log.Warn("chat session could not wake for its view; retrying", "session", id, "err", err)
		}
		// A slow native reconnect can outlive one lease interval. Extend this
		// viewer's lease only if a concurrent leave has not removed it.
		s.viewMu.Lock()
		if views := s.viewLeases[id]; views != nil {
			if _, present := views[viewID]; present {
				views[viewID] = s.now().Add(chatViewLease)
			}
		}
		s.viewMu.Unlock()
		done <- err
	}()
	timer := time.NewTimer(chatViewWakeWait)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// scheduleWakeRetry records a failed wake on the retry schedule and keeps the
// sleep marker so the retry scan finds the session. It reports false once the
// attempts are spent.
func (s *Service) scheduleWakeRetry(ctx context.Context, id domain.SessionID) bool {
	if !s.deferWake(id) {
		return false
	}
	s.keepAsleepForRetry(ctx, id)
	return true
}

func (s *Service) setViewLease(id domain.SessionID, viewID string, active bool) (newView bool) {
	s.viewMu.Lock()
	defer s.viewMu.Unlock()
	views := s.liveViewLeasesLocked(id)
	if active {
		delete(s.viewClosedAt, id)
		if views == nil {
			views = make(map[string]time.Time)
			if s.viewLeases == nil {
				s.viewLeases = make(map[domain.SessionID]map[string]time.Time)
			}
			s.viewLeases[id] = views
		}
		_, existing := views[viewID]
		newView = !existing
		views[viewID] = s.now().Add(chatViewLease)
	} else if _, exists := views[viewID]; exists {
		delete(views, viewID)
		if len(views) == 0 {
			if s.viewClosedAt == nil {
				s.viewClosedAt = make(map[domain.SessionID]time.Time)
			}
			s.viewClosedAt[id] = s.now()
		}
	}
	if len(views) == 0 {
		delete(s.viewLeases, id)
		return false
	}
	return newView
}

func (s *Service) hasChatView(id domain.SessionID) bool {
	s.viewMu.Lock()
	defer s.viewMu.Unlock()
	views := s.liveViewLeasesLocked(id)
	if s.viewClosedAt[id].Add(chatHibernateGrace).After(s.now()) {
		return true
	}
	delete(s.viewClosedAt, id)
	return len(views) != 0
}

// Callers hold viewMu. Expired leases cannot keep a provider alive after a
// renderer crash or a missed release request.
func (s *Service) liveViewLeasesLocked(id domain.SessionID) map[string]time.Time {
	views := s.viewLeases[id]
	now := s.now()
	for key, expiry := range views {
		if !expiry.After(now) {
			if s.viewClosedAt == nil {
				s.viewClosedAt = make(map[domain.SessionID]time.Time)
			}
			if expiry.After(s.viewClosedAt[id]) {
				s.viewClosedAt[id] = expiry
			}
			delete(views, key)
		}
	}
	if len(views) == 0 {
		delete(s.viewLeases, id)
		return nil
	}
	return views
}

// hibernateOptions selects why a provider is stopped. Idle hibernation is the
// developer-mode setting; a version refresh stops a provider still running a
// replaced harness release so its next start uses the installed one.
type hibernateOptions struct {
	versionRefresh bool
	// forUpdate stops an idle provider so an installer can replace its
	// executable; the session wakes on its next message or once the update ends.
	forUpdate bool
	// minIdle is how long the session must have been quiet.
	minIdle time.Duration
	// ignoreViews lets a refresh run under an open view: the user's next
	// message is about to wake the session anyway.
	ignoreViews bool
}

func (o hibernateOptions) enabled(s *Service) bool {
	if o.versionRefresh || o.forUpdate {
		return true
	}
	return s.hibernationEnabled != nil && s.hibernationEnabled()
}

// eligible is the durable-fact check, read before and again after the
// provider confirms it has no background work.
func (o hibernateOptions) eligible(ctx context.Context, s *Service, rec domain.SessionRecord) bool {
	if !rec.EligibleForChatHibernation() {
		return false
	}
	minIdle := o.minIdle
	if !o.forUpdate && !o.versionRefresh && s.hibernationPolicy != nil {
		// Idle sleep waits for the session kind's threshold (an orchestrator
		// longer than a worker) and honors its project's opt-out. An update or
		// a move to a new release stops a session only to start it again, so
		// neither applies there.
		idle, allowed := s.hibernationPolicy(ctx, rec)
		if !allowed {
			return false
		}
		minIdle = max(minIdle, idle)
	}
	if rec.Activity.LastActivityAt.Add(minIdle).After(s.now()) {
		return false
	}
	return !o.versionRefresh || (rec.StaleBinary != nil && rec.StaleBinary.SwitchFailedAt == nil)
}

func (o hibernateOptions) viewed(s *Service, id domain.SessionID) bool {
	return !o.ignoreViews && s.hasChatView(id)
}
