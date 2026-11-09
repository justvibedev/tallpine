package restartresume

import (
	"context"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/bgtasks"
	"tallpine.eu/tallpine/backend/internal/domain"
)

func withListener(tr *Tracker) *Tracker {
	return tr.WithJobs(func(id domain.SessionID) []bgtasks.Info {
		if id != "w1" {
			return nil
		}
		return []bgtasks.Info{{ID: "t1", Name: "FSEvents listener", Kind: "local_bash"}, {ID: "t2", Name: "CI watch", Kind: "monitor"}}
	})
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
	if msg := onlyMessage(t, s, "w1"); strings.Contains(msg, "old job") {
		t.Fatalf("expired job claimed: %q", msg)
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
