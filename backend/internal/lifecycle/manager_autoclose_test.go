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
	mergedBy   []domain.SessionID
	// finishedKeep and finishedErr answer the finished-worker check (B588).
	finishedKeep   string
	finishedErr    error
	finishedClosed []domain.SessionID
	// undelivered answers UndeliveredTurnResult; notices records what each close
	// notice carried and readBeforeKill whether the kill had already happened.
	undelivered    string
	notices        []string
	readBeforeKill []bool
	term           *fakeCompletionTerminator
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
func (f *fakeAutoClose) FinishedWorkerKeepReason(context.Context, domain.SessionRecord, time.Time) (string, error) {
	return f.finishedKeep, f.finishedErr
}
func (f *fakeAutoClose) UndeliveredTurnResult(context.Context, domain.SessionRecord) string {
	if f.term != nil {
		f.readBeforeKill = append(f.readBeforeKill, f.term.calls == 0)
	}
	return f.undelivered
}
func (f *fakeAutoClose) NotifyFinishedClosed(_ context.Context, rec domain.SessionRecord, undelivered string) {
	f.finishedClosed = append(f.finishedClosed, rec.ID)
	f.notices = append(f.notices, undelivered)
}
func (f *fakeAutoClose) NotifyClosed(_ context.Context, rec domain.SessionRecord, requester, mergedBy domain.SessionID, undelivered string) {
	f.notices = append(f.notices, undelivered)
	f.closed = append(f.closed, rec.ID)
	f.requesters = append(f.requesters, requester)
	f.mergedBy = append(f.mergedBy, mergedBy)
}

func autoCloseSetup(rec domain.SessionRecord) (*Manager, *fakeCompletionTerminator, *fakeAutoClose) {
	m, st, _ := newManager()
	term := &fakeCompletionTerminator{}
	hooks := &fakeAutoClose{term: term}
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

func finishedSetup(rec domain.SessionRecord) (*Manager, *fakeCompletionTerminator, *fakeAutoClose) {
	m, term, hooks := autoCloseSetup(rec)
	m.store.(*fakeStore).prs[rec.ID] = nil
	return m, term, hooks
}

// B588: a finished worker with no PR closes and its orchestrator is told, from
// the sweep alone, so workers that predate this path get the same treatment.
func TestAutoClose_FinishedWorkerClosesFromSweep(t *testing.T) {
	m, term, hooks := finishedSetup(idleWorker("fw-1"))
	hooks.sessions = []domain.SessionID{"fw-1"}
	m.RearmAutoCloses(ctx)
	if term.calls != 1 || len(hooks.finishedClosed) != 1 || len(hooks.closed) != 0 {
		t.Fatalf("kills=%d finished=%v merged=%v, want one finished close", term.calls, hooks.finishedClosed, hooks.closed)
	}
	rec := m.store.(*fakeStore).sessions["fw-1"]
	if rec.TerminateOnPRMerge {
		t.Fatal("flag still set after close: a restore by hand would close again")
	}
}

// B588: each keep-condition the lifecycle owns holds the close with no notice.
func TestAutoClose_FinishedWorkerKeeps(t *testing.T) {
	for name, set := range map[string]func(*domain.SessionRecord, *fakeAutoClose){
		"keep reason":    func(_ *domain.SessionRecord, h *fakeAutoClose) { h.finishedKeep = "unmerged commits" },
		"queued message": func(_ *domain.SessionRecord, h *fakeAutoClose) { h.queued = true },
		"backlog item":   func(_ *domain.SessionRecord, h *fakeAutoClose) { h.items = 1 },
		"running job":    func(_ *domain.SessionRecord, h *fakeAutoClose) { h.jobs = 1 },
		"turn running":   func(r *domain.SessionRecord, _ *fakeAutoClose) { r.Activity.State = domain.ActivityActive },
		"waiting input":  func(r *domain.SessionRecord, _ *fakeAutoClose) { r.Activity.State = domain.ActivityWaitingInput },
		"check failed":   func(_ *domain.SessionRecord, h *fakeAutoClose) { h.finishedErr = context.DeadlineExceeded },
	} {
		rec := idleWorker("fw-2")
		m, term, hooks := finishedSetup(rec)
		st := m.store.(*fakeStore)
		cur := st.sessions["fw-2"]
		set(&cur, hooks)
		st.sessions["fw-2"] = cur
		hooks.sessions = []domain.SessionID{"fw-2"}
		m.RearmAutoCloses(ctx)
		if term.calls != 0 || len(hooks.finishedClosed) != 0 || len(hooks.held) != 0 {
			t.Errorf("%s: kills=%d closed=%v held=%v, want none", name, term.calls, hooks.finishedClosed, hooks.held)
		}
	}
}

// B588: the per-session auto-close flag, off, disables the finished path too.
func TestAutoClose_FinishedWorkerRespectsFlagOff(t *testing.T) {
	m, term, hooks := finishedSetup(idleWorker("fw-3"))
	st := m.store.(*fakeStore)
	cur := st.sessions["fw-3"]
	cur.TerminateOnPRMerge = false
	st.sessions["fw-3"] = cur
	m.autoCloseAfterSignal(ctx, "fw-3")
	if term.calls != 0 || len(hooks.finishedClosed) != 0 {
		t.Fatalf("kills=%d, want none with the flag off", term.calls)
	}
}

// B588 review: hours of finished-worker keeps must not age the end-on-merge
// hold, or a later merge held by a job would get a false give-up notice and no
// recheck.
func TestAutoClose_FinishedKeepDoesNotStartHoldClock(t *testing.T) {
	m, term, hooks := finishedSetup(idleWorker("fw-4"))
	now := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
	m.autoCloseRetry.clock = func() time.Time { return now }
	armed := 0
	m.autoCloseRetry.after = func(time.Duration, func()) { armed++ }
	hooks.finishedKeep = "open PR"
	hooks.sessions = []domain.SessionID{"fw-4"}
	m.RearmAutoCloses(ctx)
	now = now.Add(7 * time.Hour)
	hooks.jobs = 1
	m.store.(*fakeStore).prs["fw-4"] = []domain.PullRequest{{URL: "pr1", Merged: true}}
	observeMerged(t, m, "fw-4")
	if term.calls != 0 || len(hooks.held) != 0 || armed != 1 {
		t.Fatalf("kills=%d held=%v armed=%d, want no notice and one recheck", term.calls, hooks.held, armed)
	}
}

// A sweep logs a keep once per reason; a new reason is new.
func TestAutoClose_KeptReasonDedupes(t *testing.T) {
	var r autoCloseRetry
	if !r.noteKept("s", "a") || r.noteKept("s", "a") || !r.noteKept("s", "b") {
		t.Fatal("noteKept must report only a changed reason")
	}
	r.forgetKept("s")
	if !r.noteKept("s", "b") {
		t.Fatal("a forgotten session starts fresh")
	}
}

// B557: both close paths read the result before the kill and pass it on.
func TestAutoClose_CarriesUndeliveredTurnResult(t *testing.T) {
	t.Run("all PRs merged", func(t *testing.T) {
		m, term, hooks := autoCloseSetup(idleWorker("ac-r"))
		hooks.undelivered = "the answer"
		observeMerged(t, m, "ac-r")
		if term.calls != 1 || len(hooks.notices) != 1 || hooks.notices[0] != "the answer" {
			t.Fatalf("kills=%d notices=%q", term.calls, hooks.notices)
		}
		if len(hooks.readBeforeKill) != 1 || !hooks.readBeforeKill[0] {
			t.Fatalf("read before kill = %v", hooks.readBeforeKill)
		}
	})
	t.Run("finished worker", func(t *testing.T) {
		m, term, hooks := finishedSetup(idleWorker("fw-r"))
		hooks.undelivered = "the answer"
		hooks.sessions = []domain.SessionID{"fw-r"}
		m.RearmAutoCloses(ctx)
		if term.calls != 1 || len(hooks.notices) != 1 || hooks.notices[0] != "the answer" {
			t.Fatalf("kills=%d notices=%q", term.calls, hooks.notices)
		}
		if len(hooks.readBeforeKill) != 1 || !hooks.readBeforeKill[0] {
			t.Fatalf("read before kill = %v", hooks.readBeforeKill)
		}
	})
}

// B609: a close that a later idle signal triggers (no requester on its ctx) is
// still attributed to the orchestrator whose merge led to it.
func TestAutoClose_DeferredCloseKeepsMergeRequester(t *testing.T) {
	rec := idleWorker("ac-m")
	rec.Activity.State = domain.ActivityActive
	m, term, hooks := autoCloseSetup(rec)
	c := ports.WithMergeRequester(ctx, "orch-1")
	if err := m.ApplySCMObservation(c, "ac-m", ports.SCMObservation{Fetched: true}); err != nil {
		t.Fatal(err)
	}
	if term.calls != 0 {
		t.Fatalf("kills=%d while the worker is active", term.calls)
	}
	st := m.store.(*fakeStore)
	cur := st.sessions["ac-m"]
	cur.Activity.State = domain.ActivityIdle
	st.sessions["ac-m"] = cur
	m.autoCloseAfterSignal(ctx, "ac-m") // the idle signal: no requester on ctx
	if term.calls != 1 || len(hooks.mergedBy) != 1 || hooks.mergedBy[0] != "orch-1" || hooks.requesters[0] != "" {
		t.Fatalf("kills=%d mergedBy=%v requesters=%v, want a kill attributed to orch-1 without suppressing the notice", term.calls, hooks.mergedBy, hooks.requesters)
	}
}

// B609: a remembered merge requester stops explaining a close after its TTL.
func TestAutoClose_ExpiredMergeRequesterIsIgnored(t *testing.T) {
	m, _, _ := autoCloseSetup(idleWorker("ac-x"))
	now := time.Unix(5000, 0)
	m.clock = func() time.Time { return now }
	m.NoteMergeRequester("ac-x", "orch-1")
	if got := m.mergeRequesterFor(ctx, "ac-x"); got != "orch-1" {
		t.Fatalf("fresh requester = %q", got)
	}
	now = now.Add(mergeRequesterTTL + time.Second)
	if got := m.mergeRequesterFor(ctx, "ac-x"); got != "" {
		t.Fatalf("expired requester = %q, want none", got)
	}
}
