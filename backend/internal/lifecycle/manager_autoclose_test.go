package lifecycle

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type fakeAutoClose struct {
	queued   bool
	jobs     int
	items    int
	held     []string
	sessions []domain.SessionID
	closed   []domain.SessionID
	// requesters is the requester NotifyClosed was given, per notice.
	requesters []domain.SessionID
}

func (f *fakeAutoClose) HasQueuedMessages(context.Context, domain.SessionID) (bool, error) {
	return f.queued, nil
}
func (f *fakeAutoClose) RunningJobs(context.Context, domain.SessionRecord) (int, error) {
	return f.jobs, nil
}
func (f *fakeAutoClose) OpenBacklogItems(context.Context, domain.SessionID) (int, error) {
	return f.items, nil
}
func (f *fakeAutoClose) EndOnMergeSessions(context.Context) ([]domain.SessionID, error) {
	return f.sessions, nil
}
func (f *fakeAutoClose) NotifyHeld(_ context.Context, rec domain.SessionRecord, reason string) {
	f.held = append(f.held, string(rec.ID)+": "+reason)
}
func (f *fakeAutoClose) NotifyClosed(_ context.Context, rec domain.SessionRecord, requester domain.SessionID) {
	f.closed = append(f.closed, rec.ID)
	f.requesters = append(f.requesters, requester)
}

func autoCloseSetup(rec domain.SessionRecord) (*Manager, *fakeCompletionTerminator, *fakeAutoClose) {
	m, st, _ := newManager()
	term := &fakeCompletionTerminator{}
	hooks := &fakeAutoClose{}
	m.SetCompletionTerminator(term)
	m.SetAutoCloseHooks(hooks)
	rec.TerminateOnPRMerge = true
	st.sessions[rec.ID] = rec
	st.prs[rec.ID] = []domain.PullRequest{{URL: "pr1", Merged: true}}
	return m, term, hooks
}

func observeMerged(t *testing.T, m *Manager, id domain.SessionID) {
	t.Helper()
	if err := m.ApplyPRObservation(ctx, id, ports.PRObservation{Fetched: true, URL: "pr1", Merged: true}); err != nil {
		t.Fatal(err)
	}
}

// An idle worker whose PRs are all merged is closed and its orchestrator told.
func TestAutoClose_IdleMergedWorkerClosesAndNotifies(t *testing.T) {
	m, term, hooks := autoCloseSetup(idleWorker("ac-1"))
	observeMerged(t, m, "ac-1")
	if term.calls != 1 || len(hooks.closed) != 1 || hooks.closed[0] != "ac-1" {
		t.Fatalf("kills=%d notices=%v, want one kill and one notice for ac-1", term.calls, hooks.closed)
	}
}

// B431: a message queued for the worker holds the close; nothing is killed or announced.
func TestAutoClose_QueuedMessageHoldsClose(t *testing.T) {
	m, term, hooks := autoCloseSetup(idleWorker("ac-2"))
	hooks.queued = true
	observeMerged(t, m, "ac-2")
	if term.calls != 0 || len(hooks.closed) != 0 {
		t.Fatalf("kills=%d notices=%v, want none while a message is queued", term.calls, hooks.closed)
	}
	hooks.queued = false
	observeMerged(t, m, "ac-2")
	if term.calls != 1 {
		t.Fatalf("kills=%d after the queue drained, want 1", term.calls)
	}
}

// A worker waiting on the person is not idle; closing it would drop the question.
func TestAutoClose_NeedsInputWorkerIsKept(t *testing.T) {
	rec := idleWorker("ac-3")
	rec.Activity.State = domain.ActivityWaitingInput
	m, term, hooks := autoCloseSetup(rec)
	observeMerged(t, m, "ac-3")
	if term.calls != 0 || len(hooks.closed) != 0 {
		t.Fatalf("kills=%d notices=%v, want none for a worker waiting on input", term.calls, hooks.closed)
	}
}

// The SCM observer never re-reports an unchanged merged PR, so a close held at
// merge time must be retried when the worker settles: the idle signal does it.
func TestAutoClose_HeldCloseRetriesOnIdleSignal(t *testing.T) {
	m, term, hooks := autoCloseSetup(idleWorker("ac-4"))
	hooks.queued = true
	observeMerged(t, m, "ac-4")
	if term.calls != 0 {
		t.Fatalf("kills=%d while queued, want 0", term.calls)
	}
	hooks.queued = false
	if err := m.ApplyActivitySignal(ctx, "ac-4", ports.ActivitySignal{Valid: true, State: domain.ActivityIdle, Event: "stop"}); err != nil {
		t.Fatal(err)
	}
	if term.calls != 1 || len(hooks.closed) != 1 {
		t.Fatalf("kills=%d notices=%v after the idle signal, want 1 and 1", term.calls, hooks.closed)
	}
}

// No notice for a session the kill skipped because a mutation was in progress.
func TestAutoClose_NoNoticeWhenKillSkipped(t *testing.T) {
	m, term, hooks := autoCloseSetup(idleWorker("ac-5"))
	m.SetSessionOperationGate(fixedSessionOperationGate(true))
	observeMerged(t, m, "ac-5")
	if term.calls != 0 || len(hooks.closed) != 0 {
		t.Fatalf("kills=%d notices=%v, want none while a mutation is in progress", term.calls, hooks.closed)
	}
}

// Auto-close clears the flag so a restored worker is not closed again at its
// first idle; a session that was never auto-closed (shutdown teardown) keeps it.
func TestAutoClose_ClearsFlagOnlyWhenItClosed(t *testing.T) {
	m, st, _ := newManager()
	term := &fakeCompletionTerminator{}
	m.SetCompletionTerminator(term)
	m.SetAutoCloseHooks(&fakeAutoClose{})
	for _, id := range []domain.SessionID{"ac-6", "ac-7"} {
		rec := idleWorker(id)
		rec.TerminateOnPRMerge = true
		st.sessions[id] = rec
	}
	st.prs["ac-6"] = []domain.PullRequest{{URL: "pr1", Merged: true}}
	observeMerged(t, m, "ac-6")
	if st.sessions["ac-6"].TerminateOnPRMerge {
		t.Fatal("auto-closed session kept its end-on-merge flag")
	}
	if !st.sessions["ac-7"].TerminateOnPRMerge {
		t.Fatal("an unrelated session lost its end-on-merge flag")
	}
}

// B494: a close held by a running job re-arms itself and lands once the jobs
// end, with no further merge observation or agent turn (production sends none).
func TestAutoClose_RunningJobHoldRetriesOnTimer(t *testing.T) {
	m, term, hooks := autoCloseSetup(idleWorker("ac-job"))
	var fire []func()
	m.autoCloseRetry.after = func(_ time.Duration, f func()) { fire = append(fire, f) }
	hooks.jobs = 2
	observeMerged(t, m, "ac-job")
	if term.calls != 0 || len(fire) != 1 {
		t.Fatalf("kills=%d timers=%d, want held with one timer armed", term.calls, len(fire))
	}
	fire[0]() // job still running: re-arms, still open
	if term.calls != 0 || len(fire) != 2 {
		t.Fatalf("kills=%d timers=%d, want still held and re-armed", term.calls, len(fire))
	}
	hooks.jobs = 0
	fire[1]()
	if term.calls != 1 || len(hooks.closed) != 1 {
		t.Fatalf("kills=%d notices=%v after the job ended, want one close", term.calls, hooks.closed)
	}
}

// B487: a worker with open linked backlog items is kept after its first PR merges.
func TestAutoClose_OpenBacklogItemsHoldClose(t *testing.T) {
	m, term, hooks := autoCloseSetup(idleWorker("ac-items"))
	var fire []func()
	m.autoCloseRetry.after = func(_ time.Duration, f func()) { fire = append(fire, f) }
	hooks.items = 2
	observeMerged(t, m, "ac-items")
	if term.calls != 0 || len(fire) != 1 {
		t.Fatalf("kills=%d timers=%d, want held with a recheck armed", term.calls, len(fire))
	}
	hooks.items = 0
	fire[0]()
	if term.calls != 1 {
		t.Fatalf("kills=%d after the items closed, want 1", term.calls)
	}
}

// B498: a hold past the bound notifies the orchestrator once and stops polling.
func TestAutoClose_HoldPastBoundNotifiesOnce(t *testing.T) {
	m, term, hooks := autoCloseSetup(idleWorker("ac-bound"))
	now := time.Unix(1000, 0)
	m.autoCloseRetry.clock = func() time.Time { return now }
	var fire []func()
	m.autoCloseRetry.after = func(_ time.Duration, f func()) { fire = append(fire, f) }
	hooks.jobs = 1
	observeMerged(t, m, "ac-bound")
	now = now.Add(autoCloseMaxHold + time.Minute)
	fire[0]()
	fire[0]()
	if term.calls != 0 || len(hooks.held) != 1 || len(fire) != 1 {
		t.Fatalf("kills=%d notices=%v timers=%d, want one notice and no re-arm", term.calls, hooks.held, len(fire))
	}
}

// B498: after a daemon restart the in-memory recheck is gone; RearmAutoCloses retries the close.
func TestAutoClose_RearmAfterRestartCloses(t *testing.T) {
	m, term, hooks := autoCloseSetup(idleWorker("ac-rearm"))
	hooks.sessions = []domain.SessionID{"ac-rearm"}
	m.RearmAutoCloses(ctx)
	if term.calls != 1 {
		t.Fatalf("kills=%d, want the rearm to close the idle merged worker", term.calls)
	}
}

// A hold that ended because the worker resumed work must not leak its start
// time or notified flag into the next hold.
func TestAutoClose_HoldStateResetsWhenWorkerResumes(t *testing.T) {
	m, term, hooks := autoCloseSetup(idleWorker("ac-reset"))
	st := m.store.(*fakeStore)
	now := time.Unix(1000, 0)
	m.autoCloseRetry.clock = func() time.Time { return now }
	m.autoCloseRetry.after = func(time.Duration, func()) {}
	hooks.jobs = 1
	observeMerged(t, m, "ac-reset")
	now = now.Add(autoCloseMaxHold / 2)
	rec := st.sessions["ac-reset"]
	rec.Activity.State = domain.ActivityActive
	st.sessions["ac-reset"] = rec
	observeMerged(t, m, "ac-reset") // resumed: early return clears the hold
	rec.Activity.State = domain.ActivityIdle
	st.sessions["ac-reset"] = rec
	now = now.Add(autoCloseMaxHold/2 + time.Minute)
	observeMerged(t, m, "ac-reset") // fresh hold, only a minute old
	if len(hooks.held) != 0 || term.calls != 0 {
		t.Fatalf("notices=%v kills=%d, want a fresh hold with no bound notice", hooks.held, term.calls)
	}
}

// liveOnlyTerm reports the session as already ended by someone else.
type liveOnlyTerm struct{ calls int }

func (f *liveOnlyTerm) Kill(context.Context, domain.SessionID) (bool, error) { return true, nil }
func (f *liveOnlyTerm) KillIfLive(context.Context, domain.SessionID) (bool, error) {
	f.calls++
	return false, nil
}

// B554: a session an orchestrator already killed is not announced as having
// "ended on its own".
func TestAutoClose_AlreadyKilledSessionSendsNoNotice(t *testing.T) {
	m, _, hooks := autoCloseSetup(idleWorker("ac-k"))
	term := &liveOnlyTerm{}
	m.SetCompletionTerminator(term)
	observeMerged(t, m, "ac-k")
	if term.calls != 1 || len(hooks.closed) != 0 {
		t.Fatalf("kills=%d notices=%v, want one attempt and no notice", term.calls, hooks.closed)
	}
}

// B560: the requester of the merge that caused the close reaches NotifyClosed
// (so the daemon can skip a notice to that same session); an external merge
// carries none.
func TestAutoClose_MergeRequesterReachesNotice(t *testing.T) {
	for _, requester := range []domain.SessionID{"orch-1", ""} {
		m, term, hooks := autoCloseSetup(idleWorker("ac-l"))
		c := ports.WithMergeRequester(ctx, requester)
		if err := m.ApplyPRObservation(c, "ac-l", ports.PRObservation{Fetched: true, URL: "pr1", Merged: true}); err != nil {
			t.Fatal(err)
		}
		if term.calls != 1 || len(hooks.requesters) != 1 || hooks.requesters[0] != requester {
			t.Fatalf("kills=%d requesters=%v, want one kill and requester %q", term.calls, hooks.requesters, requester)
		}
	}
}
