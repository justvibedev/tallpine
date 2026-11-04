package sessionmanager

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/sessionguard"
)

type agentOperationKind string

const (
	agentOperationExit              agentOperationKind = "exit"
	agentOperationResume            agentOperationKind = "resume"
	agentOperationHibernate         agentOperationKind = "hibernate"
	agentOperationKill              agentOperationKind = "kill"
	agentOperationRestore           agentOperationKind = "restore"
	agentOperationRetire            agentOperationKind = "retire"
	agentOperationReconcile         agentOperationKind = "reconcile"
	agentOperationInterfaceRecovery agentOperationKind = "interface_recovery"
	agentOperationProviderSwitch    agentOperationKind = "provider_switch"
)

var errAgentOperationInProgress = errors.New("session: another exclusive operation is in progress")

var _ sessionguard.InputLease = (*Manager)(nil)
var _ sessionguard.InputPreparer = (*Manager)(nil)

// PrepareSessionInput wakes a hibernated Chat ahead of a Tallpine write to it, outside
// any input lease (see sessionguard.InputPreparer). Failure is not reported
// here: the write that follows hits the same wake path and returns the error.
// Only a hibernated session is woken; one stopped on purpose stays stopped.
func (m *Manager) PrepareSessionInput(ctx context.Context, id domain.SessionID) {
	if m.chat == nil {
		return
	}
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil || !ok || rec.IsTerminated || rec.HibernatedAt == nil || domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat {
		return
	}
	if m.chat.HasLiveChatController(id) {
		return
	}
	if err := m.WakeHibernatedChat(ctx, id); err != nil {
		m.logger.Warn("wake before Tallpine write failed", "sessionID", id, "error", err)
	}
}

// AcquireSessionInput atomically admits one pane write unless an exclusive
// operation already owns the Tallpine session. The returned release is idempotent;
// callers hold it through the underlying pane write so a later mutation can
// close admission and wait for every already-admitted write to finish.
func (m *Manager) AcquireSessionInput(id domain.SessionID) (release func(), ok bool) {
	id = domain.SessionID(strings.TrimSpace(string(id)))
	m.agentOpMu.Lock()
	if m.agentOperationActiveLocked(id) {
		m.agentOpMu.Unlock()
		return nil, false
	}
	if m.inputLeases[id] == 0 {
		m.inputDrained[id] = make(chan struct{})
		if m.inputLeaseSince == nil {
			m.inputLeaseSince = make(map[domain.SessionID]time.Time)
		}
		m.inputLeaseSince[id] = time.Now()
	}
	m.inputLeases[id]++
	m.agentOpMu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() { m.releaseSessionInput(id) })
	}, true
}

func (m *Manager) releaseSessionInput(id domain.SessionID) {
	m.agentOpMu.Lock()
	defer m.agentOpMu.Unlock()
	count := m.inputLeases[id]
	if count <= 1 {
		delete(m.inputLeases, id)
		delete(m.inputLeaseSince, id)
		if drained := m.inputDrained[id]; drained != nil {
			close(drained)
			delete(m.inputDrained, id)
		}
		return
	}
	m.inputLeases[id] = count - 1
}

// SessionMutationInProgress is consumed by observation-driven lifecycle paths
// that must not independently terminate a session while Tallpine is replacing or
// relaunching its provider process. A live interface or account transition is
// one: between its source stop and the target's spawn the session's runtime is
// dead by design, and a same-mode move stays probeable all through it.
func (m *Manager) SessionMutationInProgress(id domain.SessionID) bool {
	id = domain.SessionID(strings.TrimSpace(string(id)))
	m.agentOpMu.Lock()
	active := m.agentOperationActiveLocked(id)
	m.agentOpMu.Unlock()
	if active {
		return true
	}
	m.transitionMu.Lock()
	defer m.transitionMu.Unlock()
	_, live := m.transitions[id]
	return live
}

func (m *Manager) agentOperationActiveLocked(id domain.SessionID) bool {
	_, ok := m.agentOperations[id]
	_, deferred := m.deferredInterfaceRecovery[id]
	return ok || deferred
}

// beginAgentOperation closes input admission before waiting for already-issued
// leases. Because both actions share agentOpMu, a pane write is either fully
// admitted before the operation (and drained) or rejected after it; there is
// no interval in which it can pass a boolean check and write into the new
// provider generation.
func (m *Manager) beginAgentOperation(ctx context.Context, id domain.SessionID, kind agentOperationKind) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var drained <-chan struct{}
	var killWaitUntil time.Time
	for {
		m.agentOpMu.Lock()
		if m.agentOperationActiveLocked(id) {
			current := m.agentOperations[id]
			if kind == agentOperationKill && m.takeQueuedReconcileLocked(id) {
				m.setAgentOperationLocked(id, kind)
				drained = m.inputDrained[id]
				m.agentOpMu.Unlock()
				break
			}
			killYields := kind == agentOperationKill && m.killYieldsToLocked(id, current)
			m.agentOpMu.Unlock()
			if killYields {
				// Kill queues behind automatic recovery (B541) for a bounded
				// time instead of failing at once; past it, the caller learns
				// the holder from operationInProgressError.
				if killWaitUntil.IsZero() {
					killWaitUntil = time.Now().Add(killYieldWait)
				}
				if time.Now().After(killWaitUntil) {
					return errAgentOperationInProgress
				}
			} else if current != agentOperationHibernate || kind == agentOperationHibernate {
				return errAgentOperationInProgress
			}
			// Background cleanup yields to every explicit operation once its
			// bounded shutdown completes; it must not make Kill/Switch fail.
			select {
			case <-ctx.Done():
				m.logSlotTimeout(id, kind, "waiting for the holder to yield")
				return ctx.Err()
			case <-time.After(20 * time.Millisecond):
				continue
			}
		}
		m.setAgentOperationLocked(id, kind)
		drained = m.inputDrained[id]
		m.agentOpMu.Unlock()
		break
	}
	if drained == nil {
		return nil
	}
	select {
	case <-drained:
		return nil
	case <-ctx.Done():
		m.logSlotTimeout(id, kind, "input leases did not drain")
		m.endAgentOperation(id, kind)
		return ctx.Err()
	}
}

func (m *Manager) setAgentOperationLocked(id domain.SessionID, kind agentOperationKind) {
	m.agentOperations[id] = kind
	if m.agentOperationSince == nil {
		m.agentOperationSince = make(map[domain.SessionID]time.Time)
	}
	m.agentOperationSince[id] = time.Now()
}

// logSlotTimeout names what a timed-out wait was stuck behind, so a stalled
// resume or send is diagnosable from the log alone.
func (m *Manager) logSlotTimeout(id domain.SessionID, wanted agentOperationKind, why string) {
	m.agentOpMu.Lock()
	holder, held := m.agentOperations[id]
	opAge := time.Since(m.agentOperationSince[id])
	leases := m.inputLeases[id]
	leaseAge := time.Since(m.inputLeaseSince[id])
	m.agentOpMu.Unlock()
	attrs := []any{"sessionID", id, "wanted", wanted, "reason", why, "inputLeases", leases}
	if leases > 0 {
		attrs = append(attrs, "oldestLeaseAge", leaseAge.Round(time.Millisecond))
	}
	if held {
		attrs = append(attrs, "heldBy", holder, "heldFor", opAge.Round(time.Millisecond))
	}
	logger := m.logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Warn("agent operation wait timed out", attrs...)
}

// beginAgentOperations reserves every currently-unowned session before waiting
// for any admitted input to drain. Startup reconciliation uses this batch form
// so candidates queued behind its worker limit are fenced just as early as the
// candidates already being probed.
func (m *Manager) beginAgentOperations(ctx context.Context, ids []domain.SessionID, kind agentOperationKind) ([]domain.SessionID, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	type reservation struct {
		id      domain.SessionID
		drained <-chan struct{}
	}
	reservations := make([]reservation, 0, len(ids))
	m.agentOpMu.Lock()
	for _, id := range ids {
		id = domain.SessionID(strings.TrimSpace(string(id)))
		if id == "" || m.agentOperationActiveLocked(id) {
			continue
		}
		m.setAgentOperationLocked(id, kind)
		reservations = append(reservations, reservation{id: id, drained: m.inputDrained[id]})
	}
	m.agentOpMu.Unlock()

	for _, reservation := range reservations {
		if reservation.drained == nil {
			continue
		}
		select {
		case <-reservation.drained:
		case <-ctx.Done():
			for _, reserved := range reservations {
				m.endAgentOperation(reserved.id, kind)
			}
			return nil, ctx.Err()
		}
	}
	acquired := make([]domain.SessionID, 0, len(reservations))
	for _, reservation := range reservations {
		acquired = append(acquired, reservation.id)
	}
	return acquired, nil
}

func (m *Manager) endAgentOperation(id domain.SessionID, kind agentOperationKind) {
	m.agentOpMu.Lock()
	defer m.agentOpMu.Unlock()
	if current, ok := m.agentOperations[id]; ok && current == kind {
		delete(m.agentOperations, id)
		delete(m.agentOperationSince, id)
		delete(m.reconcileQueued, id)
		m.resumeDeferredInterfaceRecoveryLocked(id)
	}
}

// The deferred fence survives the foreign operation's release. Recovery, not
// that operation, is responsible for reopening input after its durable commit.
func (m *Manager) resumeDeferredInterfaceRecoveryLocked(id domain.SessionID) {
	transitionID, deferred := m.deferredInterfaceRecovery[id]
	if !deferred {
		return
	}
	if err := m.beginAgentSwitchAttempt(); err != nil {
		return // Preserve the fence for the next boot after shutdown admission closes.
	}
	go func() {
		defer m.agentSwitchWorkers.Done()
		recovered, err := m.recoverInterfaceTransitions(m.backgroundContext, transitionID)
		if err != nil {
			m.logger.Error("interface transition: deferred recovery failed", "sessionID", id, "error", err)
		} else if len(recovered) == 0 {
			// The exact obligation was cancelled/settled before the worker read it.
			m.agentOpMu.Lock()
			if m.deferredInterfaceRecovery[id] == transitionID {
				delete(m.deferredInterfaceRecovery, id)
			}
			m.agentOpMu.Unlock()
		}
	}()
}

func (m *Manager) beginAgentResume(ctx context.Context, id domain.SessionID) error {
	waitCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	err := m.beginAgentOperation(waitCtx, id, agentOperationResume)
	if errors.Is(err, errAgentOperationInProgress) {
		return ErrResumeInProgress
	}
	return err
}

func (m *Manager) endAgentResume(id domain.SessionID) {
	m.endAgentOperation(id, agentOperationResume)
}

// resumeJoinWait bounds how long a user Resume waits for an automatic resume,
// under the ~12 s a phone client allows a request.
var resumeJoinWait = 8 * time.Second

// autoResume is resumeAgentWithMode for automatic callers (wake, startup
// recovery, version move); it marks the session so a concurrent user Resume
// may join instead of failing.
func (m *Manager) autoResume(ctx context.Context, id domain.SessionID, requireNativeHistory bool) error {
	m.agentOpMu.Lock()
	if m.autoResumes == nil {
		m.autoResumes = make(map[domain.SessionID]int)
	}
	m.autoResumes[id]++
	m.agentOpMu.Unlock()
	defer func() {
		m.agentOpMu.Lock()
		m.autoResumes[id]--
		if m.autoResumes[id] <= 0 {
			delete(m.autoResumes, id)
		}
		m.agentOpMu.Unlock()
	}()
	_, err := m.resumeAgentWithMode(ctx, id, requireNativeHistory)
	return err
}

// beginAgentResumeJoining is beginAgentResume that, when an automatic resume
// or wake owns the session, waits (at most resumeJoinWait) for it and retries.
// The caller then sees the finished launch (a live controller) and reports
// success, so a Resume pressed during an auto-resume neither errors nor starts
// a second agent. Any other holder (a user resume, a handed-off spawn retry,
// hibernate, exit, switch) fails fast with the usual 409, as does a timeout.
func (m *Manager) beginAgentResumeJoining(ctx context.Context, id domain.SessionID) error {
	waitCtx, cancel := context.WithTimeout(ctx, resumeJoinWait)
	defer cancel()
	for {
		err := m.beginAgentResume(ctx, id)
		if !errors.Is(err, ErrResumeInProgress) {
			return err
		}
		m.agentOpMu.Lock()
		joinable := m.agentOperations[id] == agentOperationResume && m.autoResumes[id] > 0
		m.agentOpMu.Unlock()
		if !joinable {
			return err
		}
		select {
		case <-waitCtx.Done():
			return err
		case <-time.After(20 * time.Millisecond):
		}
	}
}
