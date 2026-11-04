package restartresume

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	reportsvc "tallpine.eu/tallpine/backend/internal/service/report"
)

type fakeStore struct {
	recs map[domain.SessionID]domain.SessionRecord
}

func (f *fakeStore) ListAllSessions(context.Context) ([]domain.SessionRecord, error) {
	out := make([]domain.SessionRecord, 0, len(f.recs))
	for _, r := range f.recs {
		out = append(out, r)
	}
	return out, nil
}

func (f *fakeStore) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	r, ok := f.recs[id]
	return r, ok, nil
}

type fakeSender struct {
	sent   map[domain.SessionID][]string
	failed int
}

func (f *fakeSender) Send(_ context.Context, id domain.SessionID, msg string, _ *ports.SpawnAttachment) error {
	if f.failed > 0 {
		f.failed--
		return errors.New("busy")
	}
	if f.sent == nil {
		f.sent = map[domain.SessionID][]string{}
	}
	f.sent[id] = append(f.sent[id], msg)
	return nil
}

type fakeReports struct {
	ids  []string
	loud []string // IDs filed without Quiet; these would wake the orchestrator
}

func (f *fakeReports) Create(_ context.Context, in reportsvc.CreateInput) (domain.ReportRecord, error) {
	f.ids = append(f.ids, in.ID)
	if !in.Quiet {
		f.loud = append(f.loud, in.ID)
	}
	return domain.ReportRecord{}, nil
}

func worker(id string, state domain.ActivityState, terminated bool) domain.SessionRecord {
	return domain.SessionRecord{ID: domain.SessionID(id), Kind: domain.KindWorker, Mode: domain.SessionModeChat, IsTerminated: terminated, Activity: domain.Activity{State: state}}
}

func newTracker(t *testing.T, recs ...domain.SessionRecord) (*Tracker, *fakeStore) {
	t.Helper()
	st := &fakeStore{recs: map[domain.SessionID]domain.SessionRecord{}}
	for _, r := range recs {
		st.recs[r.ID] = r
	}
	tr := New(t.TempDir(), st)
	tr.interval = time.Millisecond
	return tr, st
}

func TestResumeSendsOnceToInterruptedWorkersAndSkipsStopped(t *testing.T) {
	ctx := context.Background()
	tr, st := newTracker(t,
		worker("w-1", domain.ActivityActive, false),
		worker("w-2", domain.ActivityActive, false),
		worker("w-3", domain.ActivityActive, false),
		worker("w-4", domain.ActivityIdle, false),
	)
	if err := tr.Record(ctx); err != nil {
		t.Fatal(err)
	}
	// The restart resets activity; w-2 was killed and w-3 exited meanwhile.
	st.recs["w-1"] = worker("w-1", domain.ActivityIdle, false)
	st.recs["w-2"] = worker("w-2", domain.ActivityIdle, true)
	st.recs["w-3"] = worker("w-3", domain.ActivityExited, false)

	sender, reports := &fakeSender{}, &fakeReports{}
	tr.Resume(ctx, sender)
	tr.Resume(ctx, sender) // a second boot pass must not repeat it
	tr.NotifyRestart(ctx, reports)

	if len(sender.sent) != 1 || len(sender.sent["w-1"]) != 1 {
		t.Fatalf("sent = %v, want exactly one resume message to w-1", sender.sent)
	}
	if !strings.Contains(sender.sent["w-1"][0], "interrupted by an app restart") {
		t.Fatalf("message = %q", sender.sent["w-1"][0])
	}
	if len(reports.ids) != 1 || !strings.HasPrefix(reports.ids[0], "rpt_restart_resume_w-1_") {
		t.Fatalf("reports = %v, want one report for w-1", reports.ids)
	}
	if len(reports.loud) != 0 {
		t.Fatalf("turn_resumed filed loud %v; it must be quiet (B192)", reports.loud)
	}
	if got := tr.load(); len(got) != 1 || got[0].SessionID != "w-3" {
		t.Fatalf("pending = %v, want only the exited w-3 kept for a later boot", got)
	}
}

func TestResumeRetriesRefusedSendAndKeepsEntryAcrossRecordCalls(t *testing.T) {
	ctx := context.Background()
	tr, st := newTracker(t, worker("w-1", domain.ActivityActive, false))
	if err := tr.Record(ctx); err != nil {
		t.Fatal(err)
	}
	st.recs["w-1"] = worker("w-1", domain.ActivityIdle, false)
	// A boot-time Record must not drop or duplicate the shutdown entry.
	if err := tr.Record(ctx); err != nil {
		t.Fatal(err)
	}
	if got := tr.load(); len(got) != 1 {
		t.Fatalf("pending = %v, want 1", got)
	}
	sender := &fakeSender{failed: 2}
	tr.Resume(ctx, sender)
	if len(sender.sent["w-1"]) != 1 {
		t.Fatalf("sent = %v, want delivery after two refusals", sender.sent)
	}
}

func TestResumeDropsStaleEntries(t *testing.T) {
	ctx := context.Background()
	tr, st := newTracker(t, worker("w-1", domain.ActivityActive, false))
	if err := tr.Record(ctx); err != nil {
		t.Fatal(err)
	}
	st.recs["w-1"] = worker("w-1", domain.ActivityIdle, false)
	tr.clock = func() time.Time { return time.Now().Add(maxAge + time.Hour) }
	sender := &fakeSender{}
	tr.Resume(ctx, sender)
	if len(sender.sent) != 0 {
		t.Fatalf("sent = %v, want nothing for a stale entry", sender.sent)
	}
}

func TestPlainRestartLeavesSurvivingTurnsAlone(t *testing.T) {
	ctx := context.Background()
	tr, st := newTracker(t, worker("w-1", domain.ActivityActive, false))
	// A restart that only detaches: the host keeps running the turn, which
	// finishes before the daemon is back up.
	if err := tr.RecordIfQuit(ctx, false); err != nil {
		t.Fatal(err)
	}
	st.recs["w-1"] = worker("w-1", domain.ActivityIdle, false)
	sender, reports := &fakeSender{}, &fakeReports{}
	tr.Resume(ctx, sender)
	if len(sender.sent) != 0 || len(reports.ids) != 0 {
		t.Fatalf("sent=%v reports=%v, want nothing for a turn that survived the restart", sender.sent, reports.ids)
	}
	// A deliberate quit parks the agent: that turn is cut short and resumed.
	st.recs["w-1"] = worker("w-1", domain.ActivityActive, false)
	if err := tr.RecordIfQuit(ctx, true); err != nil {
		t.Fatal(err)
	}
	st.recs["w-1"] = worker("w-1", domain.ActivityIdle, false)
	tr.Resume(ctx, sender)
	tr.NotifyRestart(ctx, reports)
	if len(sender.sent["w-1"]) != 1 {
		t.Fatalf("sent = %v, want one resume after a deliberate quit", sender.sent)
	}
}

// blockingSender holds Send open so a test can interleave a Record with a pass.
type blockingSender struct {
	entered chan struct{}
	release chan struct{}
	sent    []domain.SessionID
}

func (b *blockingSender) Send(_ context.Context, id domain.SessionID, _ string, _ *ports.SpawnAttachment) error {
	b.entered <- struct{}{}
	<-b.release
	b.sent = append(b.sent, id)
	return nil
}

func TestRecordDuringABlockedSendIsNotLost(t *testing.T) {
	ctx := context.Background()
	tr, st := newTracker(t, worker("w-1", domain.ActivityActive, false))
	if err := tr.Record(ctx); err != nil {
		t.Fatal(err)
	}
	st.recs["w-1"] = worker("w-1", domain.ActivityIdle, false)
	tr.attempts = 1 // inspect the file right after the pass that was blocked
	sender := &blockingSender{entered: make(chan struct{}), release: make(chan struct{})}
	done := make(chan struct{})
	go func() {
		defer close(done)
		tr.Resume(ctx, sender)
	}()
	<-sender.entered // the pass is mid-send
	st.recs["w-2"] = worker("w-2", domain.ActivityActive, false)
	if err := tr.Record(ctx); err != nil { // the next shutdown
		t.Fatal(err)
	}
	close(sender.release)
	<-done
	got := tr.load()
	if len(got) != 1 || got[0].SessionID != "w-2" {
		t.Fatalf("pending = %v, want only the entry recorded during the pass (w-2)", got)
	}
}

func TestWakeIdleIsIdempotentAcrossTwoDaemonStarts(t *testing.T) {
	ctx := context.Background()
	tr, st := newTracker(t,
		worker("w-idle", domain.ActivityIdle, false),
		worker("w-active", domain.ActivityActive, false),
		worker("w-dead", domain.ActivityIdle, true),
		worker("w-exited", domain.ActivityExited, false),
	)
	st.recs["orch"] = domain.SessionRecord{ID: "orch", Kind: domain.KindOrchestrator, Activity: domain.Activity{State: domain.ActivityIdle}}
	snd, rep := &fakeSender{}, &fakeReports{}
	if n := tr.WakeIdle(ctx, snd, nil); n != 1 {
		t.Fatalf("first start woke %d, want 1", n)
	}
	tr.NotifyRestart(ctx, rep)
	// Second start, worker never ran since the notice: still unanswered.
	again := New(filepathDir(tr), st)
	if n := again.WakeIdle(ctx, snd, nil); n != 0 {
		t.Fatalf("second start without activity woke %d, want 0", n)
	}
	if got := snd.sent["w-idle"]; len(got) != 1 || !strings.Contains(got[0], "background jobs and monitors you started were stopped") {
		t.Fatalf("w-idle messages = %q", got)
	}
	if len(snd.sent) != 1 || len(rep.ids) != 1 {
		t.Fatalf("sent=%v reports=%v, want only w-idle", snd.sent, rep.ids)
	}
	if len(rep.loud) != 0 {
		t.Fatalf("restart_wake filed loud %v; it must be quiet (B192)", rep.loud)
	}
	// The worker ran (and may have armed a new monitor) before the next restart.
	rec := st.recs["w-idle"]
	rec.Activity.LastActivityAt = time.Now().Add(time.Minute)
	st.recs["w-idle"] = rec
	third := New(filepathDir(tr), st)
	if n := third.WakeIdle(ctx, snd, nil); n != 1 {
		t.Fatalf("restart after activity woke %d, want 1", n)
	}
}

func TestWakeIdleSkipsWorkersWhoseHostSurvived(t *testing.T) {
	ctx := context.Background()
	tr, _ := newTracker(t, worker("kept", domain.ActivityIdle, false), worker("gone", domain.ActivityIdle, false))
	snd := &fakeSender{}
	n := tr.WakeIdle(ctx, snd, func(id domain.SessionID) bool { return id == "kept" })
	if n != 1 || len(snd.sent["kept"]) != 0 || len(snd.sent["gone"]) != 1 {
		t.Fatalf("woke %d, sent=%v: a surviving host must not be told its jobs stopped", n, snd.sent)
	}
}

func TestWakeIdleRetriesAfterFailedSend(t *testing.T) {
	ctx := context.Background()
	tr, _ := newTracker(t, worker("w-1", domain.ActivityIdle, false))
	snd := &fakeSender{failed: 1}
	if n := tr.WakeIdle(ctx, snd, nil); n != 0 {
		t.Fatalf("failed send counted as woken: %d", n)
	}
	if n := tr.WakeIdle(ctx, snd, nil); n != 1 {
		t.Fatalf("retry woke %d, want 1", n)
	}
}

func filepathDir(tr *Tracker) string { return strings.TrimSuffix(tr.path, "/"+fileName) }

func TestQuitParkedWorkerGetsOneNoticeAndEarlierHibernatedNone(t *testing.T) {
	ctx := context.Background()
	earlier := worker("earlier", domain.ActivityIdle, false)
	hibAt := time.Now().Add(-time.Hour)
	earlier.HibernatedAt = &hibAt
	tr, st := newTracker(t, worker("parked", domain.ActivityIdle, false), earlier)
	// Deliberate quit: ParkAll then marks the live controller's worker asleep.
	if err := tr.RecordIfQuit(ctx, true); err != nil {
		t.Fatal(err)
	}
	rec := st.recs["parked"]
	at := time.Now()
	rec.HibernatedAt = &at
	st.recs["parked"] = rec
	snd := &fakeSender{}
	again := New(filepathDir(tr), st) // next boot
	if n := again.WakeIdle(ctx, snd, nil); n != 1 || len(snd.sent["parked"]) != 1 || len(snd.sent["earlier"]) != 0 {
		t.Fatalf("woke %d, sent=%v: want one notice for the quit-parked worker, none for the earlier-hibernated one", n, snd.sent)
	}
}

func TestWakeIdleSkipsTUIWorkers(t *testing.T) {
	tui := worker("tui", domain.ActivityIdle, false)
	tui.Mode = domain.SessionModeTUI
	tr, _ := newTracker(t, tui)
	snd := &fakeSender{}
	if n := tr.WakeIdle(context.Background(), snd, nil); n != 0 || len(snd.sent) != 0 {
		t.Fatalf("woke %d, sent=%v: a TUI worker's terminal host survives, its jobs still run", n, snd.sent)
	}
}

func TestResumedWorkerGetsExactlyOneMessageAcrossBothPasses(t *testing.T) {
	ctx := context.Background()
	tr, st := newTracker(t, worker("w-1", domain.ActivityActive, false))
	if err := tr.Record(ctx); err != nil {
		t.Fatal(err)
	}
	rec := st.recs["w-1"]
	rec.Activity.State = domain.ActivityIdle // restart: the turn is gone, the agent reads idle
	st.recs["w-1"] = rec
	snd := &fakeSender{}
	tr.Resume(ctx, snd)
	tr.WakeIdle(ctx, snd, nil)
	if got := snd.sent["w-1"]; len(got) != 1 {
		t.Fatalf("messages across Resume+WakeIdle = %q, want exactly one", got)
	}
}

func TestParkedSetServesOnePassOnly(t *testing.T) {
	ctx := context.Background()
	tr, st := newTracker(t, worker("w", domain.ActivityActive, false))
	// Quit while w is mid-turn: it is in both the in-flight and the parked set.
	if err := tr.RecordIfQuit(ctx, true); err != nil {
		t.Fatal(err)
	}
	rec := st.recs["w"]
	at := time.Now()
	rec.HibernatedAt, rec.Activity.State = &at, domain.ActivityIdle
	st.recs["w"] = rec
	snd := &fakeSender{}
	boot := New(filepathDir(tr), st)
	boot.Resume(ctx, snd) // messages w
	if n := boot.WakeIdle(ctx, snd, nil); n != 0 {
		t.Fatalf("first pass woke %d, want 0 (Resume already messaged w)", n)
	}
	// Idle hibernation later sleeps w; a later crash restart must not revive it.
	rec.Activity.LastActivityAt = time.Now().Add(time.Hour)
	st.recs["w"] = rec
	next := New(filepathDir(tr), st)
	if n := next.WakeIdle(ctx, snd, nil); n != 0 || len(snd.sent["w"]) != 1 {
		t.Fatalf("second start woke %d, messages %q: a stale parked entry revived a hibernated worker", n, snd.sent["w"])
	}
}

// The orchestrator's turn is cut by a quit like a worker's, so it gets the same
// resume message. It gets no report: the report service rejects non-workers.
func TestResumeWakesInterruptedOrchestratorWithoutReport(t *testing.T) {
	ctx := context.Background()
	orch := func(state domain.ActivityState) domain.SessionRecord {
		return domain.SessionRecord{ID: "orch", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat, Activity: domain.Activity{State: state}}
	}
	tr, st := newTracker(t, orch(domain.ActivityActive), worker("w-1", domain.ActivityActive, false))
	if err := tr.RecordIfQuit(ctx, true); err != nil {
		t.Fatal(err)
	}
	st.recs["orch"] = orch(domain.ActivityIdle)
	st.recs["w-1"] = worker("w-1", domain.ActivityIdle, false)

	sender, reports := &fakeSender{}, &fakeReports{}
	tr.Resume(ctx, sender)
	tr.NotifyRestart(ctx, reports)

	if got := sender.sent["orch"]; len(got) != 1 || !strings.Contains(got[0], "interrupted by an app restart") {
		t.Fatalf("orchestrator messages = %q, want one resume message", got)
	}
	if len(reports.ids) != 1 || !strings.Contains(reports.ids[0], "w-1") {
		t.Fatalf("reports = %v, want only the worker's", reports.ids)
	}
}

// An unclean exit (installer kill, crash) never runs RecordIfQuit. At boot the
// mid-turn orchestrator whose host died is resumed exactly once; an idle one, a
// terminated one and one whose host survived get nothing.
func TestUncleanExitResumesInFlightOrchestratorOnce(t *testing.T) {
	ctx := context.Background()
	orch := func(id string, state domain.ActivityState, term bool) domain.SessionRecord {
		return domain.SessionRecord{ID: domain.SessionID(id), Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat, IsTerminated: term, Activity: domain.Activity{State: state}}
	}
	tr, st := newTracker(t, orch("busy", domain.ActivityActive, false), orch("idle", domain.ActivityIdle, false),
		orch("dead", domain.ActivityActive, true), orch("alive", domain.ActivityActive, false))
	if err := tr.RecordUnclean(ctx, func(id domain.SessionID) bool { return id == "alive" }); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"busy", "alive"} {
		st.recs[domain.SessionID(id)] = orch(id, domain.ActivityIdle, false)
	}
	sender := &fakeSender{}
	tr.Resume(ctx, sender)
	tr.Resume(ctx, sender)
	if len(sender.sent) != 1 || len(sender.sent["busy"]) != 1 || !strings.Contains(sender.sent["busy"][0], "not the user") {
		t.Fatalf("sent = %v, want exactly one note to busy", sender.sent)
	}
}

// The installer's sequence: a bare /shutdown (no stopAgents) records, the app
// then stops the agents, and the daemon boots with the sessions' activity reset
// to idle. The in-flight orchestrator gets one note; one whose host survived
// and one idle at shutdown get none.
func TestBareShutdownThenAgentStopResumesOrchestratorOnce(t *testing.T) {
	ctx := context.Background()
	orch := func(id string, state domain.ActivityState) domain.SessionRecord {
		return domain.SessionRecord{ID: domain.SessionID(id), Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat, Activity: domain.Activity{State: state}}
	}
	tr, st := newTracker(t, orch("busy", domain.ActivityActive), orch("idle", domain.ActivityIdle), orch("alive", domain.ActivityActive))
	if err := tr.RecordIfQuit(ctx, false); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"busy", "idle", "alive"} { // agents stopped after the daemon: state reads idle at boot
		st.recs[domain.SessionID(id)] = orch(id, domain.ActivityIdle)
	}
	if err := tr.RecordUnclean(ctx, func(id domain.SessionID) bool { return id == "alive" }); err != nil {
		t.Fatal(err)
	}
	sender := &fakeSender{}
	tr.Resume(ctx, sender)
	tr.Resume(ctx, sender)
	if len(sender.sent) != 1 || len(sender.sent["busy"]) != 1 {
		t.Fatalf("sent = %v, want exactly one note to busy", sender.sent)
	}
}

// A restored session still reading Active from before the interruption is stale
// and must still be resumed (B218 b).
func TestResumeNotesStaleActiveSession(t *testing.T) {
	ctx := context.Background()
	tr, st := newTracker(t, worker("w-1", domain.ActivityActive, false))
	if err := tr.Record(ctx); err != nil {
		t.Fatal(err)
	}
	sender := &fakeSender{}
	tr.Resume(ctx, sender)
	if len(sender.sent["w-1"]) != 1 {
		t.Fatalf("sent = %v, want one note for a stale-Active session", sender.sent)
	}
	_ = st
}

// Boot reconcile marks a dead-host chat exited and restarts it asynchronously:
// the note must wait for the restart, not be dropped (B218 review).
func TestResumeWaitsForExitedSessionToRestart(t *testing.T) {
	ctx := context.Background()
	orch := func(state domain.ActivityState) domain.SessionRecord {
		return domain.SessionRecord{ID: "orch", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat, Activity: domain.Activity{State: state}}
	}
	tr, st := newTracker(t, orch(domain.ActivityActive))
	if err := tr.RecordUnclean(ctx, nil); err != nil {
		t.Fatal(err)
	}
	st.recs["orch"] = orch(domain.ActivityExited)
	tr.store = &restartingStore{fakeStore: st, idle: orch(domain.ActivityIdle), after: 2}
	sender := &fakeSender{}
	tr.Resume(ctx, sender)
	if len(sender.sent["orch"]) != 1 {
		t.Fatalf("sent = %v, want one note once the session restarted", sender.sent)
	}
}

// restartingStore reads exited for the first `after` lookups, then idle.
type restartingStore struct {
	*fakeStore
	idle  domain.SessionRecord
	after int
	calls int
}

func (r *restartingStore) GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	r.calls++
	if r.calls > r.after {
		return r.idle, true, nil
	}
	return r.fakeStore.GetSession(ctx, id)
}

// An unclean-exit entry keeps the time of the interruption, so a late boot does
// not resume work older than maxAge (B218 review).
func TestUncleanEntryKeepsInterruptionTimeAndHonoursMaxAge(t *testing.T) {
	ctx := context.Background()
	old := time.Now().Add(-7 * time.Hour)
	rec := worker("w-1", domain.ActivityActive, false)
	rec.Activity.LastActivityAt = old
	tr, st := newTracker(t, rec)
	if err := tr.RecordUnclean(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if got := tr.load(); len(got) != 1 || !got[0].InterruptedAt.Equal(old.UTC()) {
		t.Fatalf("entries = %v, want InterruptedAt = last activity %v", got, old)
	}
	st.recs["w-1"] = worker("w-1", domain.ActivityIdle, false)
	sender := &fakeSender{}
	tr.Resume(ctx, sender)
	if len(sender.sent) != 0 {
		t.Fatalf("sent = %v, want nothing for a 7h-old interruption", sender.sent)
	}
}
