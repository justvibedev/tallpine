package review

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/service/machineload"
)

// fakeGate is a MachineGate with a settable status.
type fakeGate struct {
	mu       sync.Mutex
	status   machineload.Status
	err      error
	admits   int
	released int
}

func (g *fakeGate) set(status machineload.Status) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.status = status
}

func (g *fakeGate) Check(context.Context) (machineload.Status, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.status, g.err
}

func (g *fakeGate) Admit(ctx context.Context) (func(), machineload.Status, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.admits++
	if g.err != nil || g.status.Busy {
		return func() {}, g.status, g.err
	}
	return func() {
		g.mu.Lock()
		g.released++
		g.mu.Unlock()
	}, g.status, nil
}

func busyStatus() machineload.Status {
	return machineload.Status{Busy: true, ByCount: true, Load1: 2.5, Cores: 8, Reviewers: 3, MaxReviewers: 3, LoadPercent: 100}
}

func gatedEngine(gate *fakeGate) (*Engine, *queueStore, *fakeLauncher) {
	qs := &queueStore{fakeStore: &fakeStore{}}
	launcher := &fakeLauncher{handle: "review-mer-1"}
	eng := newEngineForTest(qs, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, launcher)
	eng.machine = gate
	return eng, qs, launcher
}

// An agent's request while the machine is busy is parked in the persisted
// queue with the reason, and nothing starts.
func TestAgentRerunWhileMachineBusyIsQueuedWithHeldReason(t *testing.T) {
	gate := &fakeGate{status: busyStatus()}
	eng, qs, launcher := gatedEngine(gate)
	res, err := eng.TriggerWithOptions(context.Background(), "mer-1", TriggerOptions{Source: domain.ReviewTriggerAgent, Rerun: true})
	if err != nil {
		t.Fatalf("err = %v, want the request queued", err)
	}
	if !res.Queued || res.Created || res.HeldReason != busyStatus().Reason() {
		t.Fatalf("result = %+v, want queued with HeldReason %q", res, busyStatus().Reason())
	}
	if len(qs.queued) != 1 || qs.queued[0].PRURL != "https://github.com/o/r/pull/1" {
		t.Fatalf("queue = %+v, want PR #1", qs.queued)
	}
	if len(qs.runs) != 0 || launcher.spawnCount != 0 {
		t.Fatalf("a held pass must not record a run or launch: runs=%+v spawns=%d", qs.runs, launcher.spawnCount)
	}
}

// An automatic pass while busy is skipped with machine_busy, and the very next
// sweep starts it once the gate opens.
func TestAutoTriggerWhileMachineBusySkipsThenStartsWhenItOpens(t *testing.T) {
	gate := &fakeGate{status: busyStatus()}
	eng, qs, launcher := gatedEngine(gate)
	res, err := triggerWithSource(context.Background(), eng, "mer-1", "", domain.AgentConfig{}, domain.ReviewTriggerAuto)
	if err != nil || res.SkipReason != SkipMachineBusy || res.Created || !strings.HasPrefix(res.HeldReason, "machine busy") {
		t.Fatalf("held result = %+v, err = %v; want SkipReason %q", res, err, SkipMachineBusy)
	}
	if !TransientAutoSkip(res.SkipReason) {
		t.Fatal("machine_busy must be a transient skip so the sweep retries it")
	}
	if len(qs.runs) != 0 || len(qs.queued) != 0 || launcher.spawnCount != 0 {
		t.Fatalf("a held auto pass must leave no trace: runs=%+v queue=%+v", qs.runs, qs.queued)
	}
	gate.set(machineload.Status{})
	res, err = triggerWithSource(context.Background(), eng, "mer-1", "", domain.AgentConfig{}, domain.ReviewTriggerAuto)
	if err != nil || !res.Created || len(qs.runs) != 1 {
		t.Fatalf("after the gate opened: result = %+v, err = %v, runs = %+v; want a started pass", res, err, qs.runs)
	}
}

// The owner's own review never waits for the machine.
func TestManualTriggerBypassesTheMachineGate(t *testing.T) {
	gate := &fakeGate{status: busyStatus()}
	eng, _, _ := gatedEngine(gate)
	res, err := triggerWithSource(context.Background(), eng, "mer-1", "", domain.AgentConfig{}, domain.ReviewTriggerManual)
	if err != nil || !res.Created {
		t.Fatalf("manual result = %+v, err = %v; want a started pass", res, err)
	}
	if gate.admits != 0 {
		t.Fatalf("a manual trigger consulted the gate %d times", gate.admits)
	}
}

// A gate that cannot read the machine fails open.
func TestMachineGateErrorDoesNotHoldAPass(t *testing.T) {
	gate := &fakeGate{err: errors.New("no load reading")}
	eng, _, _ := gatedEngine(gate)
	res, err := triggerWithSource(context.Background(), eng, "mer-1", "", domain.AgentConfig{}, domain.ReviewTriggerAuto)
	if err != nil || !res.Created {
		t.Fatalf("result = %+v, err = %v; want the pass started", res, err)
	}
}

// The reserved slot is released once the pass is recorded, so a later trigger
// is counted from the store and not twice.
func TestMachineSlotIsReleasedAfterAPassStarts(t *testing.T) {
	gate := &fakeGate{}
	eng, _, _ := gatedEngine(gate)
	if res, err := triggerWithSource(context.Background(), eng, "mer-1", "", domain.AgentConfig{}, domain.ReviewTriggerAuto); err != nil || !res.Created {
		t.Fatalf("result = %+v, err = %v", res, err)
	}
	if gate.admits != 1 || gate.released != 1 {
		t.Fatalf("admits = %d released = %d, want 1 and 1", gate.admits, gate.released)
	}
}

// `review ls` carries the reason while the machine holds queued passes.
func TestListReportsHeldReasonForQueuedPasses(t *testing.T) {
	gate := &fakeGate{status: busyStatus()}
	eng, qs, _ := gatedEngine(gate)
	qs.queued = []domain.QueuedReviewTrigger{{SessionID: "mer-1", PRURL: "https://github.com/o/r/pull/1", Harness: domain.ReviewerClaudeCode}}
	got, err := eng.List(context.Background(), "mer-1")
	if err != nil || got.HeldReason != busyStatus().Reason() {
		t.Fatalf("HeldReason = %q, err = %v; want %q", got.HeldReason, err, busyStatus().Reason())
	}
	gate.set(machineload.Status{})
	if got, _ := eng.List(context.Background(), "mer-1"); got.HeldReason != "" {
		t.Fatalf("HeldReason = %q once the machine is quiet, want empty", got.HeldReason)
	}
}

// A reviewer that already runs a pass adds no new reviewer, so the gate is not
// consulted for work handed to it.
func TestMachineGateIgnoresAReviewerThatIsAlreadyRunning(t *testing.T) {
	busyRun := requestedRun("run-1", domain.ReviewerClaudeCode, domain.ReviewRunRunning, domain.VerdictNone, 1)
	busyRun.PRURL, busyRun.TargetSHA = "https://github.com/o/r/pull/1", "oldsha"
	gate := &fakeGate{status: busyStatus()}
	qs := &queueStore{fakeStore: &fakeStore{
		reviews: map[domain.ReviewerHarness]domain.Review{
			domain.ReviewerClaudeCode: {ID: "rev-claude-code", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode, ReviewerHandleID: "claude-pane"},
		},
		runs: []domain.ReviewRun{busyRun},
	}}
	eng := newEngineForTest(qs, fakeSessions{rec: liveWorker(), ok: true}, prAt("newsha"), fakeProjects{}, &fakeLauncher{alive: true, handle: "claude-pane"})
	eng.machine = gate
	res, err := eng.TriggerWithOptions(context.Background(), "mer-1", TriggerOptions{Source: domain.ReviewTriggerAgent})
	if err != nil || res.Queued || res.SkipReason != "" {
		t.Fatalf("result = %+v, err = %v; want the work handed to the running reviewer", res, err)
	}
	if gate.admits != 0 {
		t.Fatalf("gate consulted %d times for an already running reviewer", gate.admits)
	}
}

// A trigger with nothing due neither consults the gate nor reports the machine
// as the reason.
func TestMachineGateIsNotConsultedWhenNothingIsDue(t *testing.T) {
	done := requestedRun("run-0", domain.ReviewerClaudeCode, domain.ReviewRunComplete, domain.VerdictApproved, 0)
	done.PRURL, done.TargetSHA = "https://github.com/o/r/pull/1", "sha1"
	gate := &fakeGate{status: busyStatus()}
	qs := &queueStore{fakeStore: &fakeStore{
		reviews: map[domain.ReviewerHarness]domain.Review{
			domain.ReviewerClaudeCode: {ID: "rev-claude-code", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode},
		},
		runs: []domain.ReviewRun{done},
	}}
	eng := newEngineForTest(qs, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, &fakeLauncher{handle: "review-mer-1"})
	eng.machine = gate
	res, err := triggerWithSource(context.Background(), eng, "mer-1", domain.ReviewerClaudeCode, domain.AgentConfig{}, domain.ReviewTriggerAuto)
	if err != nil || res.SkipReason == SkipMachineBusy || res.HeldReason != "" {
		t.Fatalf("result = %+v, err = %v; a reviewed head must not read as machine_busy", res, err)
	}
	if gate.admits != 0 {
		t.Fatalf("gate consulted %d times with nothing due", gate.admits)
	}
}

// B777: a request queued while the machine is busy must start with the
// requested model. An auto sweep that runs once the machine frees up must not
// start the project's default reviewer first and cover the head.
func TestAutoPassDefersToQueuedRequestSoItKeepsTheRequestedModel(t *testing.T) {
	gate := &fakeGate{status: busyStatus()}
	eng, qs, launcher := gatedEngine(gate)
	ctx := context.Background()
	opts := TriggerOptions{Source: domain.ReviewTriggerAgent, Rerun: true, Config: domain.AgentConfig{Model: "claude-opus-5-5"}}
	if res, err := eng.TriggerWithOptions(ctx, "mer-1", opts); err != nil || !res.Queued {
		t.Fatalf("result = %+v, err = %v; want the request queued", res, err)
	}
	if len(qs.queued) != 1 || qs.queued[0].Model != "claude-opus-5-5" {
		t.Fatalf("queue = %+v, want the requested model kept", qs.queued)
	}
	gate.set(machineload.Status{})
	res, err := triggerWithSource(ctx, eng, "mer-1", "", domain.AgentConfig{}, domain.ReviewTriggerAuto)
	if err != nil || res.SkipReason != SkipMachineBusy || res.Created {
		t.Fatalf("auto result = %+v, err = %v; want it held behind the queued request", res, err)
	}
	if len(qs.runs) != 0 || launcher.spawnCount != 0 {
		t.Fatalf("auto pass started despite the queued request: runs=%+v spawns=%d", qs.runs, launcher.spawnCount)
	}
}

func queueRequestThenOpenGate(t *testing.T, prs fakePRs) (*Engine, *queueStore, *fakeLauncher) {
	t.Helper()
	gate := &fakeGate{status: busyStatus()}
	qs := &queueStore{fakeStore: &fakeStore{}}
	launcher := &fakeLauncher{handle: "review-mer-1"}
	eng := newEngineForTest(qs, fakeSessions{rec: liveWorker(), ok: true}, prs, fakeProjects{}, launcher)
	eng.machine = gate
	opts := TriggerOptions{Source: domain.ReviewTriggerAgent, Rerun: true, Config: domain.AgentConfig{Model: "claude-opus-5-5"}}
	if res, err := eng.TriggerWithOptions(context.Background(), "mer-1", opts); err != nil || !res.Queued {
		t.Fatalf("result = %+v, err = %v; want the request queued", res, err)
	}
	gate.set(machineload.Status{})
	return eng, qs, launcher
}

// A queued request for another reviewer does not cover the primary reviewer's
// auto pass, so it must not hold it.
func TestAutoPassIsNotHeldByAQueuedRequestForAnotherHarness(t *testing.T) {
	eng, qs, launcher := queueRequestThenOpenGate(t, prAt("sha1"))
	qs.queued[0].Harness = domain.ReviewerCodex
	res, err := triggerWithSource(context.Background(), eng, "mer-1", "", domain.AgentConfig{}, domain.ReviewTriggerAuto)
	if err != nil || res.SkipReason != "" || launcher.spawnCount != 1 {
		t.Fatalf("result = %+v, err = %v, spawns = %d; want the auto pass started", res, err, launcher.spawnCount)
	}
}

// One PR waiting in the queue must not block auto review of the worker's
// other PRs.
func TestAutoPassStillReviewsOtherPRsWhileOneIsQueued(t *testing.T) {
	prs := fakePRs{prs: []domain.PullRequest{
		{URL: "https://github.com/o/r/pull/1", Number: 1, HeadSHA: "sha1"},
		{URL: "https://github.com/o/r/pull/2", Number: 2, HeadSHA: "sha2"},
	}}
	eng, qs, launcher := queueRequestThenOpenGate(t, prs)
	qs.queued = qs.queued[:1] // only PR #1 keeps its queued request
	res, err := triggerWithSource(context.Background(), eng, "mer-1", "", domain.AgentConfig{}, domain.ReviewTriggerAuto)
	if err != nil || res.SkipReason != "" || launcher.spawnCount != 1 {
		t.Fatalf("result = %+v, err = %v, spawns = %d; want PR #2 started", res, err, launcher.spawnCount)
	}
	for _, run := range qs.runs {
		if run.PRURL == qs.queued[0].PRURL {
			t.Fatalf("auto pass started the queued PR: %+v", run)
		}
	}
	if len(qs.runs) != 1 {
		t.Fatalf("runs = %+v, want exactly one (PR #2)", qs.runs)
	}
}
