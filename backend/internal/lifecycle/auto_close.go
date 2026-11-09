package lifecycle

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// autoCloseHooks is what ending a finished worker (B407) needs beyond the kill:
// whether a message still waits for it, and the notice to its orchestrator.
type autoCloseHooks interface {
	HasQueuedMessages(ctx context.Context, id domain.SessionID) (bool, error)
	RunningJobs(ctx context.Context, rec domain.SessionRecord) (int, error)
	// OpenBacklogItems counts live backlog items still linked to the session (B487).
	OpenBacklogItems(ctx context.Context, id domain.SessionID) (int, error)
	// UndeliveredTurnResult renders the worker's last turn result if the close
	// about to happen would lose it, or "" (B557). It must not change any state:
	// the close can still fail.
	UndeliveredTurnResult(ctx context.Context, rec domain.SessionRecord) string
	// NotifyClosed tells the orchestrator; requester is the session whose merge
	// request caused the close ("" if none), which needs no notice unless
	// undelivered carries a result it would otherwise never see.
	NotifyClosed(ctx context.Context, rec domain.SessionRecord, requester domain.SessionID, undelivered string)
	// EndOnMergeSessions lists live sessions marked end-on-merge (B498 rearm).
	EndOnMergeSessions(ctx context.Context) ([]domain.SessionID, error)
	// NotifyHeld tells the orchestrator a close has been held past the bound (B498).
	NotifyHeld(ctx context.Context, rec domain.SessionRecord, reason string)
	// FinishedWorkerKeepReason says why a worker with no merged PR must stay
	// open, or "" when it is finished with nothing unmerged (B588).
	FinishedWorkerKeepReason(ctx context.Context, rec domain.SessionRecord, now time.Time) (string, error)
	// NotifyFinishedClosed tells the orchestrator a PR-less worker was closed.
	NotifyFinishedClosed(ctx context.Context, rec domain.SessionRecord, undelivered string)
}

// SetAutoCloseHooks wires the queued-message check and the [auto] notice.
func (m *Manager) SetAutoCloseHooks(h autoCloseHooks) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.autoClose = h
}

func (m *Manager) autoCloseHooksOrNil() autoCloseHooks {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.autoClose
}

// maybeAutoClose ends a finished worker (B407). It runs on a merged or closed
// PR observation and again whenever the session settles (idle signal, workload
// exit): the SCM observer acknowledges an observation once lifecycle returns
// nil and skips unchanged PRs afterwards, so a close deferred here must be
// retried from a signal that fires later.
func (m *Manager) maybeAutoClose(ctx context.Context, id domain.SessionID) error {
	held, err := m.tryAutoClose(ctx, id)
	if !held {
		// Any outcome but a hold (resumed work, closed, gone) ends the hold's
		// start time and its once-only notice.
		m.autoCloseRetry.clear(id)
	}
	return err
}

func (m *Manager) tryAutoClose(ctx context.Context, id domain.SessionID) (held bool, err error) {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil || !ok {
		return false, err
	}
	if rec.IsTerminated || !rec.TerminateOnPRMerge {
		m.autoCloseRetry.forgetKept(id)
		return false, nil
	}
	// A merge must not race the still-working agent (#2879). A session whose
	// agent is still ActivityActive is genuinely mid-climb: with one PR merged
	// it is very likely raising the next PR in the same session, and
	// flag-terminating it now (#2811's flag-only lane) would drop it from the
	// SCM observer roster — the follow-up PR would never be attributed,
	// enriched, or nudged alert. Defer the completed-session teardown until the
	// agent has actually quiesced: the observer keeps polling this live session
	// regardless because it is still observed, so the next observation of the
	// still-merged PR re-runs this reaction after the agent exits and lands the
	// termination then. The is_terminated / needs-input dead-session gates that
	// the rest of reactions.go consults (see ApplyRuntimeObservation) still
	// bound this lane at each poll; only the "sessionComplete reads PR rows"
	// bar must additionally wait on genuine agent quiescence.
	// A session waiting on the person is not idle either: ending it would drop
	// the question.
	if rec.Activity.State == domain.ActivityActive || rec.Activity.State.NeedsInput() {
		return false, nil
	}
	done, err := m.sessionComplete(ctx, id)
	if err != nil {
		return false, err
	}
	if !done {
		return m.tryCloseFinishedWorker(ctx, rec)
	}
	// A message queued for the session must not die with it (B431): hold the
	// close, and the idle signal after it drains retries.
	undelivered := ""
	if hooks := m.autoCloseHooksOrNil(); hooks != nil {
		queued, err := hooks.HasQueuedMessages(ctx, id)
		if err != nil {
			return false, fmt.Errorf("check queued messages for %s: %w", id, err)
		}
		if queued {
			m.holdAutoClose(ctx, rec, hooks, "unanswered input (queued message or open turn)", 1)
			return true, nil
		}
		// Open backlog items linked to the session are work it still owns
		// (B487): its next PR for one of them is not yet raised.
		items, err := hooks.OpenBacklogItems(ctx, id)
		if err != nil {
			return false, fmt.Errorf("check backlog items for %s: %w", id, err)
		}
		if items > 0 {
			m.holdAutoClose(ctx, rec, hooks, "open linked backlog items", items)
			return true, nil
		}
		// An idle worker waiting on its own background job is not finished (B494).
		jobs, err := hooks.RunningJobs(ctx, rec)
		if err != nil {
			return false, fmt.Errorf("check background jobs for %s: %w", id, err)
		}
		if jobs > 0 {
			m.holdAutoClose(ctx, rec, hooks, "running background jobs", jobs)
			return true, nil
		}
		// Read last, and before the kill: the close makes the normal notice undeliverable.
		undelivered = hooks.UndeliveredTurnResult(ctx, rec)
	}
	slog.Info("auto-close firing", "session", id, "reason", "all PRs merged or closed, idle, nothing queued, no jobs")
	killed, err := m.terminateCompletedSession(ctx, id)
	if err != nil || !killed {
		return false, err
	}
	// Ended by auto-close: a later restore (by hand) must not end it again at
	// its first idle while its PRs are still merged. Sessions torn down at
	// shutdown keep the flag.
	if err := m.mutate(ctx, id, func(cur domain.SessionRecord, _ time.Time) (domain.SessionRecord, bool) {
		cur.TerminateOnPRMerge = false
		return cur, true
	}); err != nil {
		slog.Warn("clear end-on-merge after auto-close failed", "session", id, "err", err)
	}
	if hooks := m.autoCloseHooksOrNil(); hooks != nil {
		hooks.NotifyClosed(ctx, rec, ports.MergeRequester(ctx), undelivered)
	}
	return false, nil
}

// autoCloseAfterSignal retries the close once the session has settled. It runs
// outside the session lock and never fails the signal that triggered it.
func (m *Manager) autoCloseAfterSignal(ctx context.Context, id domain.SessionID) {
	if err := m.maybeAutoClose(ctx, id); err != nil {
		slog.Warn("auto-close check failed", "session", id, "err", err)
	}
}

// autoCloseJobRecheck is how often a held close is retried; autoCloseMaxHold
// bounds how long before the orchestrator is told and polling stops (B498).
const (
	autoCloseJobRecheck = time.Minute
	autoCloseMaxHold    = 6 * time.Hour
)

// holdAutoClose records a held close, logs it when the reason is new, arms a
// recheck (no signal fires when a job ends and the SCM observer will not replay
// an unchanged merge), and past the bound tells the orchestrator and gives up.
func (m *Manager) holdAutoClose(ctx context.Context, rec domain.SessionRecord, hooks autoCloseHooks, reason string, n int) {
	id := rec.ID
	first, changed := m.autoCloseRetry.note(id, reason)
	if changed {
		slog.Info("auto-close held", "session", id, "reason", reason, "count", n)
	}
	if m.autoCloseRetry.now().Sub(first) >= autoCloseMaxHold {
		if m.autoCloseRetry.markNotified(id) {
			hooks.NotifyHeld(ctx, rec, reason)
		}
		return
	}
	m.autoCloseRetry.arm(id, func() { m.autoCloseAfterSignal(context.Background(), id) })

}

// RearmAutoCloses retries the close of every idle worker marked end-on-merge:
// rechecks are in memory, so a daemon restart would otherwise forget them.
func (m *Manager) RearmAutoCloses(ctx context.Context) {
	hooks := m.autoCloseHooksOrNil()
	if hooks == nil {
		return
	}
	ids, err := hooks.EndOnMergeSessions(ctx)
	if err != nil {
		slog.Warn("auto-close rearm: list sessions failed", "err", err)
		return
	}
	for _, id := range ids {
		m.autoCloseAfterSignal(ctx, id)
	}
}

type heldClose struct {
	first    time.Time
	reason   string
	notified bool
}

// autoCloseRetry keeps at most one pending recheck per session and the state
// of each hold.
type autoCloseRetry struct {
	mu      sync.Mutex
	pending map[domain.SessionID]bool
	held    map[domain.SessionID]*heldClose
	// kept is the last logged reason a finished worker stayed open (B588). It
	// is separate from held so a keep never ages an end-on-merge hold.
	kept map[domain.SessionID]string
	// after schedules f; nil uses time.AfterFunc. clock is time.Now when nil.
	// Tests replace them.
	after func(d time.Duration, f func())
	clock func() time.Time
}

func (r *autoCloseRetry) now() time.Time {
	if r.clock != nil {
		return r.clock()
	}
	return time.Now()
}

// note records a hold and returns when it began and whether reason is new.
func (r *autoCloseRetry) note(id domain.SessionID, reason string) (time.Time, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.held == nil {
		r.held = map[domain.SessionID]*heldClose{}
	}
	h := r.held[id]
	if h == nil {
		h = &heldClose{first: r.now()}
		r.held[id] = h
	}
	changed := h.reason != reason
	h.reason = reason
	return h.first, changed
}

func (r *autoCloseRetry) markNotified(id domain.SessionID) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	h := r.held[id]
	if h == nil || h.notified {
		return false
	}
	h.notified = true
	return true
}

func (r *autoCloseRetry) clear(id domain.SessionID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.held, id)
}

// noteKept records why a finished worker stayed open and reports whether that
// reason is new, so a sweep logs each one once.
func (r *autoCloseRetry) noteKept(id domain.SessionID, reason string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.kept == nil {
		r.kept = map[domain.SessionID]string{}
	}
	changed := r.kept[id] != reason
	r.kept[id] = reason
	return changed
}

func (r *autoCloseRetry) forgetKept(id domain.SessionID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.kept, id)
}

func (r *autoCloseRetry) arm(id domain.SessionID, f func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pending[id] {
		return
	}
	if r.pending == nil {
		r.pending = map[domain.SessionID]bool{}
	}
	r.pending[id] = true
	after := r.after
	if after == nil {
		after = func(d time.Duration, f func()) { time.AfterFunc(d, f) }
	}
	after(autoCloseJobRecheck, func() {
		r.mu.Lock()
		delete(r.pending, id)
		r.mu.Unlock()
		f()
	})
}
