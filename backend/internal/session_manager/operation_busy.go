package sessionmanager

import (
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// killYieldWait bounds how long Kill waits behind an automatic operation
// (status reconcile, auto-resume) before refusing. It stays under the REST
// request timeout and covers statusVerificationLimit, so a reconcile that
// started just before the kill finishes inside the wait (B541).
var killYieldWait = 35 * time.Second

// OperationInProgressError is ErrOperationInProgress naming the operation that
// holds the session and how long it has held it, so a caller can tell a
// short automatic recovery from a wedged switch instead of blind-retrying.
type OperationInProgressError struct {
	Holder  string
	HeldFor time.Duration
}

func (e *OperationInProgressError) Error() string {
	if e.Holder == "" {
		return ErrOperationInProgress.Error()
	}
	return fmt.Sprintf("%s (%s, running for %s)", ErrOperationInProgress.Error(), e.Holder, e.HeldFor.Round(time.Second))
}

// Is lets errors.Is(err, ErrOperationInProgress) match, so existing callers
// and the API mapping keep working.
func (e *OperationInProgressError) Is(target error) bool { return target == ErrOperationInProgress }

// operationInProgressError snapshots the session's current holder. Callers map
// errAgentOperationInProgress through it; a holder that ended in between
// yields the bare sentinel text.
func (m *Manager) operationInProgressError(id domain.SessionID) error {
	m.agentOpMu.Lock()
	defer m.agentOpMu.Unlock()
	holder, held := m.agentOperations[id]
	if !held {
		if _, deferred := m.deferredInterfaceRecovery[id]; deferred {
			return &OperationInProgressError{Holder: string(agentOperationInterfaceRecovery)}
		}
		return &OperationInProgressError{}
	}
	label := string(holder)
	if holder == agentOperationResume && m.autoResumes[id] > 0 {
		label = "automatic resume"
	}
	return &OperationInProgressError{Holder: label, HeldFor: time.Since(m.agentOperationSince[id])}
}

// mapOperationInProgress turns the internal busy error into the public one
// carrying holder details; any other error passes through.
func (m *Manager) mapOperationInProgress(id domain.SessionID, err error) error {
	if errors.Is(err, errAgentOperationInProgress) {
		return m.operationInProgressError(id)
	}
	return err
}

// killYieldsToLocked reports whether a Kill should wait for the current holder
// rather than fail: automatic background work that ends on its own within
// statusVerificationLimit or its resume budget. Caller holds agentOpMu.
func (m *Manager) killYieldsToLocked(id domain.SessionID, current agentOperationKind) bool {
	switch current {
	case agentOperationReconcile:
		return true
	case agentOperationResume:
		return m.autoResumes[id] > 0
	}
	return false
}

// markReconcileQueued flags reserved reconcile slots that no worker has
// started. A batch pass reserves every candidate up front, so a session queued
// behind slow probes would otherwise hold "reconcile" for the queue wait plus
// statusVerificationLimit, longer than killYieldWait.
func (m *Manager) markReconcileQueued(recs []domain.SessionRecord) {
	m.agentOpMu.Lock()
	defer m.agentOpMu.Unlock()
	if m.reconcileQueued == nil {
		m.reconcileQueued = make(map[domain.SessionID]struct{})
	}
	for _, rec := range recs {
		if m.agentOperations[rec.ID] == agentOperationReconcile {
			m.reconcileQueued[rec.ID] = struct{}{}
		}
	}
}

// startQueuedReconcile is the worker's dequeue: it reports whether the pass
// still owns the slot, clearing the queued mark so Kill now waits instead.
func (m *Manager) startQueuedReconcile(id domain.SessionID) bool {
	m.agentOpMu.Lock()
	defer m.agentOpMu.Unlock()
	delete(m.reconcileQueued, id)
	return m.agentOperations[id] == agentOperationReconcile
}

// takeQueuedReconcileLocked hands a not-yet-started reconcile slot to the
// caller. Caller holds agentOpMu.
func (m *Manager) takeQueuedReconcileLocked(id domain.SessionID) bool {
	if _, queued := m.reconcileQueued[id]; !queued || m.agentOperations[id] != agentOperationReconcile {
		return false
	}
	delete(m.reconcileQueued, id)
	return true
}
