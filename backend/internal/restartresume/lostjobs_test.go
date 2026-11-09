package restartresume

import (
	"context"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/bgtasks"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func withListener(tr *Tracker) *Tracker {
	return tr.WithJobs(func(id domain.SessionID) []bgtasks.Info {
		if id != "w1" {
			return nil
		}
		return []bgtasks.Info{{ID: "t1", Name: "FSEvents listener", Kind: "local_bash"}, {ID: "t2", Name: "CI watch", Kind: "monitor"}}
	})
}

// seedLostJobs records a killed job for each session, as a shutdown would.
func seedLostJobs(tr *Tracker, ids ...domain.SessionID) {
	all := tr.loadLostJobs()
	for _, id := range ids {
		all[id] = lostJobs{At: time.Now(), Jobs: []lostJob{{Name: "CI watch", Kind: "monitor"}}}
	}
	tr.saveLostJobs(all)
}

func onlyMessage(t *testing.T, s *fakeSender, id domain.SessionID) string {
	t.Helper()
	if len(s.sent[id]) != 1 {
		t.Fatalf("messages to %s = %v, want exactly one", id, s.sent[id])
	}
	return s.sent[id][0]
}

func assertNamesJobs(t *testing.T, msg string) {
	t.Helper()
	for _, want := range []string{`"FSEvents listener" (local_bash)`, `"CI watch" (monitor)`, "no longer running"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message %q does not contain %q", msg, want)
		}
	}
}

func TestQuitTellsMidTurnWorkerWhichJobsWereKilled(t *testing.T) {
	ctx := context.Background()
	tr, _ := newTracker(t, worker("w1", domain.ActivityActive, false))
	withListener(tr)
	if err := tr.RecordIfQuit(ctx, true); err != nil {
		t.Fatal(err)
	}
	s := &fakeSender{}
	tr.Resume(ctx, s)
	assertNamesJobs(t, onlyMessage(t, s, "w1"))
	if left := tr.loadLostJobs(); len(left) != 0 {
		t.Fatalf("lost jobs after delivery = %v, want none", left)
	}
}

func TestQuitTellsIdleWorkerWhichJobsWereKilled(t *testing.T) {
	ctx := context.Background()
	tr, _ := newTracker(t, worker("w1", domain.ActivityIdle, false))
	withListener(tr)
	if err := tr.RecordIfQuit(ctx, true); err != nil {
		t.Fatal(err)
	}
	s := &fakeSender{}
	tr.WakeIdle(ctx, s, nil)
	assertNamesJobs(t, onlyMessage(t, s, "w1"))
}

// B614: an idle orchestrator that a quit put to sleep with a watch running was
// left unprompted until the owner wrote. The job list is the real tracker's,
// fed the frame claude-agent-acp sends for a background shell.
func TestIdleOrchestratorIsPingedOnceAboutItsKilledJob(t *testing.T) {
	ctx := context.Background()
	orch := domain.SessionRecord{ID: "o1", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityIdle}}
	tr, st := newTracker(t, orch)
	running := bgtasks.New(nil, nil, 0)
	running.Apply("o1", ports.ChatAsyncTask{ID: "btfjzd6l0", Name: "watch production sync retries", TaskType: "shell", State: ports.ChatAsyncTaskRunning})
	tr.WithJobs(running.Running)
	if err := tr.RecordIfQuit(ctx, true); err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	orch.HibernatedAt = &at // ParkAll put it to sleep
	st.recs["o1"] = orch

	s, reports := &fakeSender{}, &fakeReports{}
	boot := New(filepathDir(tr), st)
	if n := boot.WakeIdle(ctx, s, nil); n != 1 {
		t.Fatalf("woke %d, want 1", n)
	}
	boot.NotifyRestart(ctx, reports)
	msg := onlyMessage(t, s, "o1")
	for _, want := range []string{`"watch production sync retries" (shell)`, "no longer running", "your workers' states"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message %q does not contain %q", msg, want)
		}
	}
	if len(reports.ids) != 0 {
		t.Fatalf("reports = %v: the report service only takes worker reports", reports.ids)
	}

	// A crash loop restarts the daemon again before the agent has run.
	if n := New(filepathDir(tr), st).WakeIdle(ctx, s, nil); n != 0 || len(s.sent["o1"]) != 1 {
		t.Fatalf("second start woke %d, messages %q: want the one ping only", n, s.sent["o1"])
	}
}

func TestIdleAgentsThatLostNoJobAreNotPinged(t *testing.T) {
	ctx := context.Background()
	orch := domain.SessionRecord{ID: "o1", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityIdle}}
	tr, _ := newTracker(t, orch, worker("w1", domain.ActivityIdle, false))
	if err := tr.RecordIfQuit(ctx, true); err != nil {
		t.Fatal(err)
	}
	s := &fakeSender{}
	if n := tr.WakeIdle(ctx, s, nil); n != 0 || len(s.sent) != 0 {
		t.Fatalf("woke %d, sent=%v: an agent with no lost job must not be messaged", n, s.sent)
	}
}

func TestBlockedWorkerIsToldOnlyWhenItLostJobs(t *testing.T) {
	ctx := context.Background()
	tr, _ := newTracker(t, worker("w1", domain.ActivityBlocked, false), worker("w2", domain.ActivityBlocked, false))
	withListener(tr)
	if err := tr.RecordIfQuit(ctx, true); err != nil {
		t.Fatal(err)
	}
	s := &fakeSender{}
	tr.WakeIdle(ctx, s, nil)
	assertNamesJobs(t, onlyMessage(t, s, "w1"))
	if got := s.sent["w2"]; len(got) != 0 {
		t.Fatalf("blocked worker with no lost jobs was messaged: %v", got)
	}
}

func TestBareShutdownNamesJobsWhenHostDied(t *testing.T) {
	ctx := context.Background()
	tr, _ := newTracker(t, worker("w1", domain.ActivityActive, false))
	withListener(tr)
	if err := tr.RecordIfQuit(ctx, false); err != nil {
		t.Fatal(err)
	}
	if err := tr.RecordUnclean(ctx, func(domain.SessionID) bool { return false }); err != nil {
		t.Fatal(err)
	}
	s := &fakeSender{}
	tr.Resume(ctx, s)
	assertNamesJobs(t, onlyMessage(t, s, "w1"))
}

func TestSurvivingHostsJobsAreNeverReportedKilled(t *testing.T) {
	ctx := context.Background()
	tr, _ := newTracker(t, worker("w1", domain.ActivityIdle, false))
	withListener(tr)
	if err := tr.RecordIfQuit(ctx, false); err != nil {
		t.Fatal(err)
	}
	survived := func(domain.SessionID) bool { return true }
	if err := tr.RecordUnclean(ctx, survived); err != nil {
		t.Fatal(err)
	}
	if left := tr.loadLostJobs(); len(left) != 0 {
		t.Fatalf("lost jobs for a surviving host = %v, want none", left)
	}
	// A resume entry left by an earlier boot must not pick the jobs up either.
	if err := tr.save([]Entry{{SessionID: "w1", InterruptedAt: time.Now().UTC()}}); err != nil {
		t.Fatal(err)
	}
	s := &fakeSender{}
	tr.Resume(ctx, s)
	if msg := onlyMessage(t, s, "w1"); strings.Contains(msg, "FSEvents listener") {
		t.Fatalf("surviving job reported as killed: %q", msg)
	}
}

func TestFailedSendKeepsTheJobListForTheRetry(t *testing.T) {
	ctx := context.Background()
	tr, _ := newTracker(t, worker("w1", domain.ActivityActive, false))
	withListener(tr)
	if err := tr.RecordIfQuit(ctx, true); err != nil {
		t.Fatal(err)
	}
	s := &fakeSender{failed: 1}
	tr.Resume(ctx, s)
	assertNamesJobs(t, onlyMessage(t, s, "w1"))
}

func TestExpiredJobListIsNotClaimed(t *testing.T) {
	ctx := context.Background()
	tr, _ := newTracker(t, worker("w1", domain.ActivityIdle, false))
	tr.saveLostJobs(map[domain.SessionID]lostJobs{"w1": {At: time.Now().Add(-2 * maxAge), Jobs: []lostJob{{Name: "old job"}}}})
	s := &fakeSender{}
	tr.WakeIdle(ctx, s, nil)
	if len(s.sent) != 0 {
		t.Fatalf("expired job list must not ping: %v", s.sent)
	}
	if left := tr.loadLostJobs(); len(left) != 0 {
		t.Fatalf("expired entry kept: %v", left)
	}
}

func TestResumeJobsNoticeMatchesWhatTheQuitActuallyStops(t *testing.T) {
	chat := worker("c", domain.ActivityActive, false)
	if got := resumeJobsNotice(chat, nil); !strings.Contains(got, "background jobs") {
		t.Fatalf("chat worker with unknown jobs gets no warning: %q", got)
	}
	tui := chat
	tui.Mode = domain.SessionModeTUI
	if got := resumeJobsNotice(tui, nil); got != "" {
		t.Fatalf("TUI worker's jobs outlive the quit, got claim %q", got)
	}
}

func TestLostJobsSentenceFlattensAndCapsNames(t *testing.T) {
	jobs := []lostJob{{Name: "line one\n  line two"}}
	for range maxNamedJobs + 2 {
		jobs = append(jobs, lostJob{Name: strings.Repeat("x", maxJobNameRunes+10)})
	}
	got := lostJobsSentence(jobs)
	if strings.Contains(got, "\n") || !strings.Contains(got, `"line one line two"`) {
		t.Fatalf("name not flattened: %q", got)
	}
	if !strings.Contains(got, "and 3 more") {
		t.Fatalf("list not capped: %q", got)
	}
	if strings.Contains(got, strings.Repeat("x", maxJobNameRunes+1)) {
		t.Fatalf("name not truncated: %q", got)
	}
}

func TestResumeDoesNotClaimAnExpiredJobList(t *testing.T) {
	ctx := context.Background()
	tr, _ := newTracker(t, worker("w1", domain.ActivityIdle, false))
	tr.saveLostJobs(map[domain.SessionID]lostJobs{"w1": {At: time.Now().Add(-2 * maxAge), Jobs: []lostJob{{Name: "old job"}}}})
	if err := tr.save([]Entry{{SessionID: "w1", InterruptedAt: time.Now().UTC()}}); err != nil {
		t.Fatal(err)
	}
	s := &fakeSender{}
	tr.Resume(ctx, s)
	if msg := onlyMessage(t, s, "w1"); strings.Contains(msg, "old job") {
		t.Fatalf("expired job claimed: %q", msg)
	}
}

// B617: a job that outlived one restart (its host survived) is re-seeded into
// the live tracker, so a later quit that kills the host still names it, unless
// the job ended or the host went away in between.
func survivedThenQuit(t *testing.T, mutate func(*bgtasks.Tracker)) *fakeSender {
	t.Helper()
	ctx := context.Background()
	live := bgtasks.New(nil, nil, 0)
	tr, _ := newTracker(t, worker("w1", domain.ActivityIdle, false))
	tr.WithJobs(func(id domain.SessionID) []bgtasks.Info {
		if id != "w1" {
			return nil
		}
		return []bgtasks.Info{{ID: "t1", Name: "FSEvents listener", Kind: "local_bash"}, {ID: "t2", Name: "CI watch", Kind: "monitor"}}
	}).WithRestore(live.Restore)
	if err := tr.RecordIfQuit(ctx, false); err != nil {
		t.Fatal(err)
	}
	if err := tr.RecordUnclean(ctx, func(domain.SessionID) bool { return true }); err != nil {
		t.Fatal(err)
	}
	// The new daemon's tracker only knows what was re-seeded.
	tr.WithJobs(live.Running)
	mutate(live)
	if err := tr.RecordIfQuit(ctx, true); err != nil {
		t.Fatal(err)
	}
	s := &fakeSender{}
	tr.WakeIdle(ctx, s, nil)
	return s
}

func TestJobThatSurvivedARestartIsStillNamedAtALaterQuit(t *testing.T) {
	s := survivedThenQuit(t, func(*bgtasks.Tracker) {})
	assertNamesJobs(t, onlyMessage(t, s, "w1"))
}

func TestSurvivedJobThatEndedIsNotReportedKilled(t *testing.T) {
	s := survivedThenQuit(t, func(live *bgtasks.Tracker) {
		live.Apply("w1", ports.ChatAsyncTask{ID: "t1", State: ports.ChatAsyncTaskCompleted})
	})
	msg := onlyMessage(t, s, "w1")
	if strings.Contains(msg, "FSEvents listener") || !strings.Contains(msg, "CI watch") {
		t.Fatalf("message %q, want only the still-running job", msg)
	}
}

func TestSurvivedJobsOfAReplacedHostAreNotReportedKilled(t *testing.T) {
	s := survivedThenQuit(t, func(live *bgtasks.Tracker) { live.Clear("w1") })
	if got := s.sent["w1"]; len(got) != 0 {
		t.Fatalf("messages %v, want none for jobs that died with the old host", got)
	}
}

// An undelivered entry from an earlier quit names jobs of a host that is gone;
// a later plain restart with a new, live host must not resurrect them.
func TestStaleEntryFromEarlierQuitIsNotRestoredByLaterRestart(t *testing.T) {
	ctx := context.Background()
	live := bgtasks.New(nil, nil, 0)
	tr, _ := newTracker(t, worker("w1", domain.ActivityIdle, false))
	withListener(tr).WithRestore(live.Restore)
	if err := tr.RecordIfQuit(ctx, true); err != nil { // quit A records the jobs
		t.Fatal(err)
	}
	tr.WithJobs(func(domain.SessionID) []bgtasks.Info { return nil })
	if err := tr.RecordIfQuit(ctx, false); err != nil { // plain restart B: nothing running
		t.Fatal(err)
	}
	if err := tr.RecordUnclean(ctx, func(domain.SessionID) bool { return true }); err != nil {
		t.Fatal(err)
	}
	if got := live.Running("w1"); len(got) != 0 {
		t.Fatalf("restored %v from a stale entry, want none", got)
	}
}

// A background subagent's end cannot be reported after a reconnect, so a
// surviving one must not be re-seeded (it would never leave the tracker).
func TestSurvivedSubagentIsNotRestored(t *testing.T) {
	ctx := context.Background()
	live := bgtasks.New(nil, nil, 0)
	tr, _ := newTracker(t, worker("w1", domain.ActivityIdle, false))
	tr.WithJobs(func(domain.SessionID) []bgtasks.Info {
		return []bgtasks.Info{{ID: "s1", Name: "reviewer", Kind: ports.ChatAsyncTaskSubagent}, {ID: "t1", Name: "CI watch", Kind: "monitor"}}
	}).WithRestore(live.Restore)
	if err := tr.RecordIfQuit(ctx, false); err != nil {
		t.Fatal(err)
	}
	if err := tr.RecordUnclean(ctx, func(domain.SessionID) bool { return true }); err != nil {
		t.Fatal(err)
	}
	got := live.Running("w1")
	if len(got) != 1 || got[0].ID != "t1" {
		t.Fatalf("restored %v, want only the monitor", got)
	}
}
