package sessionmanager

import (
	"context"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// A slow or unavailable provider must not leave the board loading forever.
// This is a verification deadline, not an artificial delay before publication.
const statusVerificationLimit = 30 * time.Second

type statusRecovery struct {
	pending bool
	failed  *domain.SessionRecord
}

// StatusRecoveryRevision fences API snapshots read concurrently with recovery.
func (m *Manager) StatusRecoveryRevision() uint64 {
	m.statusRecoveryMu.RLock()
	defer m.statusRecoveryMu.RUnlock()
	return m.statusRecoveryRevision
}

// SessionStatusReadiness describes this daemon's recovery observation. It is
// deliberately not durable: a new daemon must verify the sessions again.
func (m *Manager) SessionStatusReadiness(rec domain.SessionRecord) string {
	m.statusRecoveryMu.RLock()
	defer m.statusRecoveryMu.RUnlock()
	result, found := m.statusRecoveries[rec.ID]
	if found {
		// A deadline cancels the recovery context, but the dependency must return
		// before another attempt can safely own this session. Keep the UI checking
		// until the operation gate has actually been released.
		if result.pending {
			return "checking"
		}
		if result.failed != nil && !rec.IsTerminated && rec.Activity.State != domain.ActivityExited &&
			rec.ControllerOwner() == result.failed.ControllerOwner() && rec.Activity == result.failed.Activity {
			return "unavailable"
		}
		return "ready"
	}
	// Startup recovery only verifies live controllers. A terminated session has
	// nothing to verify, so it never waits on the startup reconcile (which has
	// to probe the runtime of every terminated session to reap leaks).
	if rec.IsTerminated {
		return "ready"
	}
	select {
	case <-m.startupBackgroundReconcileDone:
		if m.statusRecoveryFailed {
			return "unavailable"
		}
		return "ready"
	default:
	}
	return "checking"
}

// SubscribeStatusReadiness registers fn to be called whenever a session's
// readiness may have changed (a recovery finished, or the startup reconcile
// settled). It replaces client polling: readiness is process-local and not in
// change_log, so this is a live-only signal and fn must not block. A client that
// subscribes after a change reads the settled value from its next snapshot.
func (m *Manager) SubscribeStatusReadiness(fn func()) (unsubscribe func()) {
	m.statusReadinessSubsMu.Lock()
	defer m.statusReadinessSubsMu.Unlock()
	if m.statusReadinessSubs == nil {
		m.statusReadinessSubs = map[int]func(){}
	}
	id := m.statusReadinessNextSub
	m.statusReadinessNextSub++
	m.statusReadinessSubs[id] = fn
	return func() {
		m.statusReadinessSubsMu.Lock()
		delete(m.statusReadinessSubs, id)
		m.statusReadinessSubsMu.Unlock()
	}
}

// notifyStatusReadinessChanged must be called without statusRecoveryMu held.
func (m *Manager) notifyStatusReadinessChanged() {
	m.statusReadinessSubsMu.Lock()
	subs := make([]func(), 0, len(m.statusReadinessSubs))
	for _, fn := range m.statusReadinessSubs {
		subs = append(subs, fn)
	}
	m.statusReadinessSubsMu.Unlock()
	for _, fn := range subs {
		fn()
	}
}

// settleStartupReadiness marks the startup reconcile finished (successfully
// unless resultErr is set) and tells subscribers sessions that were "checking"
// can now be read as ready.
func (m *Manager) settleStartupReadiness(resultErr error) {
	m.statusRecoveryMu.Lock()
	m.statusRecoveryFailed = resultErr != nil
	m.statusRecoveryRevision++
	m.startupBackgroundReconcileOnce.Do(func() { close(m.startupBackgroundReconcileDone) })
	if !m.statusRecoveryFailed {
		for id, result := range m.statusRecoveries {
			if statusRecoveryRedundant(result) {
				delete(m.statusRecoveries, id)
			}
		}
	}
	m.statusRecoveryMu.Unlock()
	m.notifyStatusReadinessChanged()
}

func (m *Manager) beginStatusRecovery(id domain.SessionID) {
	m.statusRecoveryMu.Lock()
	defer m.statusRecoveryMu.Unlock()
	m.statusRecoveries[id] = statusRecovery{pending: true}
	m.statusRecoveryRevision++
}

// markFreshSessionStatusReady keeps a session created by this daemon outside
// the startup snapshot's failure state. A concurrent startup recovery remains
// authoritative once it has already claimed the same session.
func (m *Manager) markFreshSessionStatusReady(id domain.SessionID) {
	m.statusRecoveryMu.Lock()
	defer m.statusRecoveryMu.Unlock()
	select {
	case <-m.startupBackgroundReconcileDone:
		// After a successful startup, an untracked session already reads as ready;
		// avoid turning ordinary spawns into recovery-revision changes.
		if !m.statusRecoveryFailed {
			return
		}
	default:
	}
	if result, found := m.statusRecoveries[id]; found && result.pending {
		return
	}
	m.statusRecoveries[id] = statusRecovery{}
	m.statusRecoveryRevision++
}

func (m *Manager) finishStatusRecovery(ctx context.Context, before domain.SessionRecord, recoveryErr error) {
	result := statusRecovery{}
	if recoveryErr != nil {
		current, found, err := m.store.GetSession(ctx, before.ID)
		if err == nil && found {
			before = current
		}
		result.failed = &before
	}
	m.statusRecoveryMu.Lock()
	if statusRecoveryRedundant(result) && m.startupSettledReady() {
		delete(m.statusRecoveries, before.ID)
	} else {
		m.statusRecoveries[before.ID] = result
	}
	m.statusRecoveryRevision++
	m.statusRecoveryMu.Unlock()
	m.notifyStatusReadinessChanged()
}

// statusRecoveryRedundant reports a settled, successful recovery. Once the
// startup reconcile has succeeded it reads exactly like an untracked session
// ("ready"), so keeping it would only grow the map with every session this
// daemon ever recovered or spawned.
func statusRecoveryRedundant(result statusRecovery) bool {
	return !result.pending && result.failed == nil
}

// startupSettledReady must be called with statusRecoveryMu held.
func (m *Manager) startupSettledReady() bool {
	select {
	case <-m.startupBackgroundReconcileDone:
		return !m.statusRecoveryFailed
	default:
		return false
	}
}
