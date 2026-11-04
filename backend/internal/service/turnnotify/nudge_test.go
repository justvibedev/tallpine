package turnnotify

import (
	"context"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/procmem"
)

type fakeNudger struct {
	sent   []string
	refuse bool
}

func (f *fakeNudger) Nudge(_ context.Context, _ domain.SessionID, message string) (bool, error) {
	if f.refuse {
		return false, nil
	}
	f.sent = append(f.sent, message)
	return true, nil
}

type fakeJobs struct{ running int }

func (f *fakeJobs) RunningJobs(context.Context, domain.SessionRecord) (int, error) {
	return f.running, nil
}

type nudgeHarness struct {
	*harness
	nudger *fakeNudger
	jobs   *fakeJobs
	now    time.Time
}

func newNudgeHarness() *nudgeHarness {
	h := &nudgeHarness{harness: newHarness(domain.KindWorker), nudger: &fakeNudger{}, jobs: &fakeJobs{},
		now: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)}
	r := h.sessions["w1"]
	r.Activity.State = domain.ActivityIdle
	h.sessions["w1"] = r
	h.o.NudgeDelay, h.o.Nudger, h.o.Jobs = 10*time.Minute, h.nudger, h.jobs
	h.o.Now = func() time.Time { return h.now }
	return h
}

func (h *nudgeHarness) setState(state domain.ActivityState) {
	r := h.sessions["w1"]
	r.Activity.State = state
	h.sessions["w1"] = r
}

func TestSilentIdleWorkerIsNudgedOnce(t *testing.T) {
	h := newNudgeHarness()
	h.signal("user-prompt-submit", "")
	h.signal("stop", "waiting for review")
	h.fire() // turn_ended report and the nudge check share a pass here
	if len(h.reports.got) != 1 || len(h.nudger.sent) != 1 {
		t.Fatalf("reports = %d, nudges = %d, want 1 and 1", len(h.reports.got), len(h.nudger.sent))
	}
	if msg := h.nudger.sent[0]; !strings.HasPrefix(msg, "[auto] idle check") || !strings.Contains(msg, "tallpine report") {
		t.Fatalf("nudge = %q", msg)
	}
	h.signal("stop", "again")
	h.fire()
	if len(h.nudger.sent) != 1 {
		t.Fatalf("nudges = %d, want 1", len(h.nudger.sent))
	}
}

func TestNudgeWaitsForBackgroundJobToFinish(t *testing.T) {
	h := newNudgeHarness()
	h.jobs.running = 1
	h.signal("user-prompt-submit", "")
	h.signal("stop", "build started in the background")
	h.fire()
	h.fire()
	if len(h.nudger.sent) != 0 {
		t.Fatalf("nudged while the job runs: %q", h.nudger.sent)
	}
	if h.delay != DefaultNudgePoll {
		t.Fatalf("re-check delay = %v, want %v", h.delay, DefaultNudgePoll)
	}
	h.jobs.running = 0
	h.fire()
	if len(h.nudger.sent) != 1 || !strings.Contains(h.nudger.sent[0], "background job") {
		t.Fatalf("nudges = %q", h.nudger.sent)
	}
}

func TestNudgeStopsWaitingOnAJobThatNeverEnds(t *testing.T) {
	h := newNudgeHarness()
	h.jobs.running = 1
	h.signal("stop", "dev server up")
	h.fire()
	h.now = h.now.Add(DefaultNudgeMaxWait)
	h.fire()
	h.jobs.running = 0
	h.fire()
	if len(h.nudger.sent) != 0 || len(h.pending) != 0 {
		t.Fatalf("nudges = %q, pending = %d; want none after the wait cap", h.nudger.sent, len(h.pending))
	}
}

func TestNudgeSkipsWorkersThatReportedOrAreNotIdle(t *testing.T) {
	cases := map[string]func(h *nudgeHarness){
		"reported": func(h *nudgeHarness) { h.o.ReportFiled(domain.ReportRecord{SessionID: "w1"}) },
		"messaged orchestrator": func(h *nudgeHarness) {
			h.sessions["o1"] = domain.SessionRecord{ID: "o1", ProjectID: "p", Kind: domain.KindOrchestrator}
			h.o.MessageSent(context.Background(), "w1", "o1")
		},
		"waiting input": func(h *nudgeHarness) { h.setState(domain.ActivityWaitingInput) },
		"active":        func(h *nudgeHarness) { h.setState(domain.ActivityActive) },
		"new turn":      func(h *nudgeHarness) { h.signal("user-prompt-submit", "") },
		"disabled":      func(h *nudgeHarness) { h.o.NudgeDelay = 0 },
	}
	for name, mutate := range cases {
		h := newNudgeHarness()
		if name == "disabled" {
			mutate(h)
		}
		h.signal("stop", "")
		if name != "disabled" {
			mutate(h)
		}
		h.fire()
		if len(h.nudger.sent) != 0 {
			t.Fatalf("%s: nudges = %q, want none", name, h.nudger.sent)
		}
	}
}

func TestNudgeIsNotRepeatedUntilTheWorkerReports(t *testing.T) {
	h := newNudgeHarness()
	h.signal("stop", "")
	h.fire()
	// The nudge opens a turn; the worker goes quiet again.
	h.signal("user-prompt-submit", "")
	h.signal("stop", "")
	h.fire()
	if len(h.nudger.sent) != 1 {
		t.Fatalf("nudges = %d, want 1 per silent episode", len(h.nudger.sent))
	}
	// Someone else gives it work: a new episode may be nudged again.
	h.signal("user-prompt-submit", "")
	h.signal("stop", "")
	h.fire()
	if len(h.nudger.sent) != 2 {
		t.Fatalf("nudges = %d, want 2 after new work", len(h.nudger.sent))
	}
}

func TestRefusedNudgeIsNotCounted(t *testing.T) {
	h := newNudgeHarness()
	h.nudger.refuse = true
	h.signal("stop", "")
	h.fire()
	h.nudger.refuse = false
	h.signal("user-prompt-submit", "")
	h.signal("stop", "")
	h.fire()
	if len(h.nudger.sent) != 1 {
		t.Fatalf("nudges = %d, want 1", len(h.nudger.sent))
	}
}

func TestProcessJobsCountsShellsBelowTheAgent(t *testing.T) {
	table, err := procmem.Parse(strings.Join([]string{
		"  10     1  100 0:01.00 -zsh",                                  // pane login shell
		"  11    10  100 0:01.00 claude --dangerously-skip-permissions", // agent
		"  12    11  100 0:01.00 node /opt/mcp/server.js",               // MCP helper
		"  13    11  100 0:01.00 /bin/zsh -c -l source snap.sh && eval 'npm run build'",
		"  14    13  100 0:01.00 node build.js",
		"  20     1  100 0:01.00 -zsh", // another session's idle pane
		"  21    20  100 0:01.00 /bin/bash -c sleep 9",
	}, "\n"))
	if err != nil {
		t.Fatal(err)
	}
	jobs := &ProcessJobs{
		Roots: func(_ context.Context, rec domain.SessionRecord) []int {
			return map[domain.SessionID][]int{"busy": {10}, "idle": {20}}[rec.ID]
		},
		Snapshot: func(context.Context) (*procmem.Table, error) { return table, nil },
	}
	for id, want := range map[domain.SessionID]int{"busy": 1, "idle": 0, "none": 0} {
		got, err := jobs.RunningJobs(context.Background(), domain.SessionRecord{ID: id})
		if err != nil || got != want {
			t.Fatalf("%s: jobs = %d, %v; want %d", id, got, err, want)
		}
	}
}

// The nudge's own turn can start before Nudge returns (a fast prompt hook);
// it must still be recognized as the nudge's turn.
type syncTurnNudger struct{ h *nudgeHarness }

func (n syncTurnNudger) Nudge(_ context.Context, _ domain.SessionID, message string) (bool, error) {
	n.h.nudger.sent = append(n.h.nudger.sent, message)
	n.h.signal("user-prompt-submit", "")
	return true, nil
}

func TestNudgeTurnStartingBeforeSendReturnsDoesNotReArm(t *testing.T) {
	h := newNudgeHarness()
	h.o.Nudger = syncTurnNudger{h}
	h.signal("stop", "")
	h.fire()
	h.signal("stop", "")
	h.fire()
	if len(h.nudger.sent) != 1 {
		t.Fatalf("nudges = %d, want 1", len(h.nudger.sent))
	}
}

func TestStaleTurnEndedSkippedWhileWorkerBusy(t *testing.T) {
	h := newNudgeHarness()
	h.signal("stop", "old turn")
	h.setState(domain.ActivityActive)
	h.fire()
	if len(h.reports.got) != 0 {
		t.Fatalf("reports = %d, want none", len(h.reports.got))
	}
}

// A TUI worker that ends its turn with a question reaches waiting_input before
// the idle timer fires; its turn_ended must still reach the orchestrator.
func TestTurnEndedReportedWhenWorkerWaitingInput(t *testing.T) {
	h := newNudgeHarness()
	h.signal("stop", "which option?")
	h.setState(domain.ActivityWaitingInput)
	h.fire()
	if len(h.reports.got) != 1 {
		t.Fatalf("reports = %d, want the turn_ended", len(h.reports.got))
	}
}

func TestChatWorkerIsNotNudged(t *testing.T) {
	h := newNudgeHarness()
	r := h.sessions["w1"]
	r.Mode, r.Metadata.ControllerGeneration = domain.SessionModeChat, "g1"
	h.sessions["w1"] = r
	_ = h.o.ApplyActivitySignal(context.Background(), "w1", ports.ActivitySignal{Event: "stop", ControllerGeneration: "g1", FinalAssistantMessage: "done"})
	h.fire()
	if len(h.reports.got) != 1 {
		t.Fatalf("reports = %d, want the chat turn_ended (path must run)", len(h.reports.got))
	}
	if len(h.nudger.sent) != 0 {
		t.Fatalf("nudges = %q, want none", h.nudger.sent)
	}
}

func TestChatTurnEndedHasShortQuietDelay(t *testing.T) {
	h := newNudgeHarness()
	r := h.sessions["w1"]
	r.Mode, r.Metadata.ControllerGeneration = domain.SessionModeChat, "g1"
	h.sessions["w1"] = r
	_ = h.o.ApplyActivitySignal(context.Background(), "w1", ports.ActivitySignal{Event: "stop", ControllerGeneration: "g1", FinalAssistantMessage: "done"})
	h.fire()
	if len(h.reports.got) != 1 || !h.reports.got[0].Quiet || h.reports.got[0].QuietFor != ChatQuietDelay {
		t.Fatalf("reports = %+v, want one quiet chat turn_ended with QuietFor=ChatQuietDelay", h.reports.got)
	}
}

func TestTurnEndedHeldWhileBackgroundJobRunsThenSurfaces(t *testing.T) {
	h := newNudgeHarness()
	h.jobs.running = 1
	h.signal("user-prompt-submit", "")
	h.signal("stop", "all done here")
	h.fire()
	h.fire() // still running
	if len(h.reports.got) != 0 {
		t.Fatalf("turn_ended surfaced with a live job: %d reports (B362)", len(h.reports.got))
	}
	h.jobs.running = 0
	h.fire() // job ended: one more quiet window, not an immediate card
	if len(h.reports.got) != 0 {
		t.Fatalf("reports = %d right as the job ended; want a quiet window first", len(h.reports.got))
	}
	h.fire()
	if len(h.reports.got) != 1 {
		t.Fatalf("reports = %d, want the held turn_ended once the worker stayed silent", len(h.reports.got))
	}
}

func TestWorkerResumingAfterJobCancelsHeldTurnEnded(t *testing.T) {
	h := newNudgeHarness()
	h.jobs.running = 1
	h.signal("user-prompt-submit", "")
	h.signal("stop", "all done here")
	h.fire()
	h.jobs.running = 0
	h.fire()
	h.signal("user-prompt-submit", "") // the worker follows up on its own
	h.fire()
	if len(h.reports.got) != 0 {
		t.Fatalf("reports = %d, want none after the worker resumed", len(h.reports.got))
	}
}

func TestHoldResetsBetweenTurns(t *testing.T) {
	h := newNudgeHarness()
	for turn := 0; turn < 2; turn++ {
		h.signal("user-prompt-submit", "")
		h.signal("stop", "Pushed. Waiting for lint to finish.")
		h.fire()
		if len(h.reports.got) != 0 {
			t.Fatalf("turn %d: reports = %d, want the hold to apply to every turn", turn, len(h.reports.got))
		}
		h.now = h.now.Add(waitTextGrace + time.Minute)
		h.fire()
		h.reports.got = nil
	}
}

func TestTurnEndedHeldWhenLastMessageWaitsOnOwnJob(t *testing.T) {
	h := newNudgeHarness()
	h.signal("user-prompt-submit", "")
	h.signal("stop", "Pushed. Waiting for lint to finish.")
	h.fire()
	if len(h.reports.got) != 0 {
		t.Fatalf("reports = %d, want none while the worker waits on its own job", len(h.reports.got))
	}
	h.now = h.now.Add(waitTextGrace + time.Minute)
	h.fire()
	if len(h.reports.got) != 1 {
		t.Fatalf("reports = %d, want the turn_ended after the grace window", len(h.reports.got))
	}
}

func TestWaitingOnYourReviewIsNotHeld(t *testing.T) {
	h := newNudgeHarness()
	h.signal("user-prompt-submit", "")
	h.signal("stop", "Waiting for your review; tests pass.")
	h.fire()
	if len(h.reports.got) != 1 {
		t.Fatalf("reports = %d, want the turn_ended: the worker waits on the orchestrator", len(h.reports.got))
	}
}
