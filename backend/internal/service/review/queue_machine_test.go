package review

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	reviewcore "tallpine.eu/tallpine/backend/internal/review"
	"tallpine.eu/tallpine/backend/internal/service/machineload"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

// stubGate is a machine-load gate with a settable status.
type stubGate struct{ status machineload.Status }

func (g *stubGate) Check(context.Context) (machineload.Status, error) { return g.status, nil }
func (g *stubGate) Admit(context.Context) (func(), machineload.Status, error) {
	return func() {}, g.status, nil
}

func gatedQueueService(t *testing.T, gate *stubGate, workers ...domain.SessionID) *Service {
	t.Helper()
	ctx := context.Background()
	st := sqlitetest.MustOpen(t)
	for i, w := range workers {
		seedNoticeWorker(t, st, w)
		q := domain.QueuedReviewTrigger{SessionID: w, PRURL: "https://github.com/o/r/pull/" + string(rune('1'+i)), Harness: domain.ReviewerCodex, CreatedAt: time.Unix(900+int64(i), 0).UTC()}
		if err := st.EnqueueReviewTrigger(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	engine := reviewcore.New(reviewcore.Deps{Store: st, MachineLoad: gate})
	return New(engine, st, WithClock(func() time.Time { return time.Unix(1000, 0).UTC() }))
}

// While the machine is busy the drain starts nothing and does not count the
// wait as a failed attempt.
func TestDrainWhileMachineBusyStartsNothingAndCountsNoAttempt(t *testing.T) {
	gate := &stubGate{status: machineload.Status{Busy: true, ByLoad: true, Load1: 9, Cores: 8, MaxReviewers: 3, LoadPercent: 100}}
	svc := gatedQueueService(t, gate, "w-1")
	calls := 0
	svc.engineTrigger = func(context.Context, domain.SessionID, reviewcore.TriggerOptions) (reviewcore.TriggerResult, error) {
		calls++
		return reviewcore.TriggerResult{}, nil
	}
	svc.drainQueuedTriggers(context.Background())
	svc.drainQueuedTriggers(context.Background())
	if calls != 0 {
		t.Fatalf("drain ran %d triggers while the machine was busy", calls)
	}
	queued, _ := svc.engine.QueuedTriggers(context.Background())
	if len(queued) != 1 || queued[0].Attempts != 0 || !queued[0].NextAttemptAt.Equal(time.Unix(900, 0).UTC()) {
		t.Fatalf("queue = %+v, want the entry kept untouched (no attempt, no backoff)", queued)
	}
}

// With the machine open the drain starts at most one queued pass per sweep.
func TestDrainStartsAtMostOneQueuedPassPerSweepWhenOpen(t *testing.T) {
	gate := &stubGate{status: machineload.Status{MaxReviewers: 3, LoadPercent: 100, Cores: 8, Load1: 0.1}}
	svc := gatedQueueService(t, gate, "w-1", "w-2", "w-3")
	var started []domain.SessionID
	svc.engineTrigger = func(_ context.Context, id domain.SessionID, _ reviewcore.TriggerOptions) (reviewcore.TriggerResult, error) {
		started = append(started, id)
		return reviewcore.TriggerResult{Created: true}, nil
	}
	svc.drainQueuedTriggers(context.Background())
	if len(started) != 1 || started[0] != "w-1" {
		t.Fatalf("first sweep started %v, want only the oldest (w-1)", started)
	}
	svc.drainQueuedTriggers(context.Background())
	if len(started) != 2 || started[1] != "w-2" {
		t.Fatalf("second sweep started %v, want w-2 next", started)
	}
	if got := queueLen(t, svc); got != 1 {
		t.Fatalf("queue length = %d, want the unstarted pass kept", got)
	}
}

// A machine-held trigger result keeps the entry and counts no attempt.
func TestDrainKeepsAPassTheTriggerHeldForTheMachine(t *testing.T) {
	gate := &stubGate{}
	svc := gatedQueueService(t, gate, "w-1")
	svc.engineTrigger = func(context.Context, domain.SessionID, reviewcore.TriggerOptions) (reviewcore.TriggerResult, error) {
		return reviewcore.TriggerResult{Queued: true, HeldReason: "machine busy"}, nil
	}
	svc.drainQueuedTriggers(context.Background())
	queued, _ := svc.engine.QueuedTriggers(context.Background())
	if len(queued) != 1 || queued[0].Attempts != 0 {
		t.Fatalf("queue = %+v, want the entry kept with no attempt", queued)
	}
}

// While the machine stays busy a pass still ages out of the queue and its
// worker is told, so a request cannot wait forever unseen.
func TestDrainDropsAnOldPassEvenWhileMachineIsBusy(t *testing.T) {
	gate := &stubGate{status: machineload.Status{Busy: true, ByCount: true, MaxReviewers: 3}}
	svc := gatedQueueService(t, gate, "w-1")
	svc.clock = func() time.Time { return time.Unix(900, 0).UTC().Add(queuedTriggerMaxAge + time.Hour) }
	svc.drainQueuedTriggers(context.Background())
	if got := queueLen(t, svc); got != 0 {
		t.Fatalf("queue length = %d, want the day-old pass dropped", got)
	}
}

// retryAfterMachineHold ends one pass of the given source without a verdict and
// runs the failure sweep with the engine answering result to the retry.
func retryAfterMachineHold(t *testing.T, source domain.ReviewTriggerSource, result reviewcore.TriggerResult) (*fakeFailureReports, *Service) {
	t.Helper()
	ctx := context.Background()
	st := sqlitetest.MustOpen(t)
	seedNoticeWorker(t, st, "w-1")
	seedRetryPR(t, st, "sha-a")
	t0 := time.Unix(1000, 0).UTC()
	run := domain.ReviewRun{ID: "run-1", ReviewID: "rev-w-1", SessionID: "w-1", Harness: domain.ReviewerCodex, Model: "gpt-5.5",
		PRURL: "https://github.com/o/r/pull/7", TargetSHA: "sha-a", Status: domain.ReviewRunRunning, CreatedAt: t0, TriggerSource: source}
	if err := st.InsertReviewRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if ok, err := st.UpdateReviewRunResult(ctx, "run-1", domain.ReviewRunFailed, domain.VerdictNone, "reviewer turn ended without submitting a result", "", false); err != nil || !ok {
		t.Fatalf("end run: %v %v", ok, err)
	}
	reports := &fakeFailureReports{}
	svc := New(reviewcore.New(reviewcore.Deps{Store: st}), st, WithClock(func() time.Time { return t0.Add(time.Hour) }))
	svc.SetFailureNotice(st, reports, &fakeWorkerNudger{})
	svc.engineTrigger = func(context.Context, domain.SessionID, reviewcore.TriggerOptions) (reviewcore.TriggerResult, error) {
		return result, nil
	}
	if err := svc.NotifyEndedRuns(ctx); err != nil {
		t.Fatal(err)
	}
	return reports, svc
}

// A retry the machine holds is parked, not announced to the worker as a final
// failure, whether the held pass was automatic or an agent's.
func TestRetryHeldForTheMachineIsParkedNotAnnouncedAsFailed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source domain.ReviewTriggerSource
		result reviewcore.TriggerResult
		parked int
	}{
		{"auto skipped machine_busy", domain.ReviewTriggerAuto, reviewcore.TriggerResult{SkipReason: reviewcore.SkipMachineBusy, HeldReason: "machine busy"}, 1},
		{"agent already queued", domain.ReviewTriggerAgent, reviewcore.TriggerResult{Queued: true, HeldReason: "machine busy"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reports, svc := retryAfterMachineHold(t, tc.source, tc.result)
			if len(reports.inputs) != 0 {
				t.Fatalf("a held retry was announced as failed: %+v", reports.inputs)
			}
			if got := queueLen(t, svc); got != tc.parked {
				t.Fatalf("queue length = %d, want %d", got, tc.parked)
			}
		})
	}
}

// The owner's own queued review does not wait for a busy machine, and it does
// not use up the sweep's one gated start.
func TestDrainStartsAManualEntryWhileMachineIsBusyAndKeepsTheRest(t *testing.T) {
	gate := &stubGate{status: machineload.Status{Busy: true, ByCount: true, MaxReviewers: 3}}
	svc := gatedQueueService(t, gate, "w-1", "w-2")
	ctx := context.Background()
	manual := domain.QueuedReviewTrigger{SessionID: "w-3", PRURL: "https://github.com/o/r/pull/3", Harness: domain.ReviewerCodex, CreatedAt: time.Unix(950, 0).UTC(), TriggerSource: domain.ReviewTriggerManual}
	seedNoticeWorker(t, svc.store.(*sqlite.Store), "w-3")
	if err := svc.store.(*sqlite.Store).EnqueueReviewTrigger(ctx, manual); err != nil {
		t.Fatal(err)
	}
	var started []domain.SessionID
	svc.engineTrigger = func(_ context.Context, id domain.SessionID, o reviewcore.TriggerOptions) (reviewcore.TriggerResult, error) {
		started = append(started, id)
		return reviewcore.TriggerResult{Created: true}, nil
	}
	svc.drainQueuedTriggers(ctx)
	if len(started) != 1 || started[0] != "w-3" {
		t.Fatalf("started %v while busy, want only the manual entry (w-3)", started)
	}
	gate.status = machineload.Status{MaxReviewers: 3}
	started = nil
	svc.drainQueuedTriggers(ctx)
	if len(started) != 1 || started[0] != "w-1" {
		t.Fatalf("started %v once open, want the oldest gated entry (w-1) alone", started)
	}
}

// A manual start is not gate-admitted, so it does not use up the sweep's one
// gated start.
func TestDrainManualStartDoesNotConsumeTheGatedStart(t *testing.T) {
	gate := &stubGate{status: machineload.Status{MaxReviewers: 3, LoadPercent: 100}}
	svc := gatedQueueService(t, gate, "w-1")
	ctx := context.Background()
	st := svc.store.(*sqlite.Store)
	seedNoticeWorker(t, st, "w-2")
	manual := domain.QueuedReviewTrigger{SessionID: "w-2", PRURL: "https://github.com/o/r/pull/3", Harness: domain.ReviewerCodex, CreatedAt: time.Unix(800, 0).UTC(), TriggerSource: domain.ReviewTriggerManual}
	if err := st.EnqueueReviewTrigger(ctx, manual); err != nil {
		t.Fatal(err)
	}
	var started []domain.SessionID
	svc.engineTrigger = func(_ context.Context, id domain.SessionID, _ reviewcore.TriggerOptions) (reviewcore.TriggerResult, error) {
		started = append(started, id)
		return reviewcore.TriggerResult{Created: true}, nil
	}
	svc.drainQueuedTriggers(ctx)
	if len(started) != 2 || started[0] != "w-2" || started[1] != "w-1" {
		t.Fatalf("started %v, want the manual entry and then the gated one", started)
	}
}
