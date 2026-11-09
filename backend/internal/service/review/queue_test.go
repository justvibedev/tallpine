package review

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	reviewcore "tallpine.eu/tallpine/backend/internal/review"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

// B394: the queue lives in SQLite, so a pass queued before a restart is still
// there for the first sweep after it. It starts when its reviewer is free,
// waits while the reviewer is busy, and is dropped once it has been tried.
func TestSweepStartsQueuedPassesWhenReviewerIsFree(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.MustOpen(t)
	seedNoticeWorker(t, st, "w-1")
	pr := "https://github.com/o/r/pull/7"
	if err := st.EnqueueReviewTrigger(ctx, domain.QueuedReviewTrigger{SessionID: "w-1", PRURL: pr, Harness: domain.ReviewerCodex, CreatedAt: time.Unix(1, 0).UTC()}); err != nil {
		t.Fatal(err)
	}
	// The reviewer is busy on another head.
	seedEndedRun(t, st, "busy", "w-1", "busysha", domain.ReviewRunRunning, "", time.Unix(2, 0).UTC())

	// A restarted daemon: a fresh service over the same store.
	svc := New(reviewcore.New(reviewcore.Deps{Store: st}), st)
	var got []reviewcore.TriggerOptions
	svc.engineTrigger = func(_ context.Context, _ domain.SessionID, o reviewcore.TriggerOptions) (reviewcore.TriggerResult, error) {
		got = append(got, o)
		return reviewcore.TriggerResult{Created: true}, nil
	}
	svc.drainQueuedTriggers(ctx)
	if len(got) != 0 {
		t.Fatalf("started %+v while the reviewer was busy", got)
	}
	if q, _ := st.ListQueuedReviewTriggers(ctx); len(q) != 1 {
		t.Fatalf("queue = %+v, want the pass kept while busy", q)
	}

	if ok, err := st.UpdateReviewRunResult(ctx, "busy", domain.ReviewRunFailed, domain.VerdictNone, "done", "", false); err != nil || !ok {
		t.Fatalf("finish busy run: %v %v", ok, err)
	}
	svc.drainQueuedTriggers(ctx)
	if len(got) != 1 || got[0].PRURL != pr || got[0].Harness != domain.ReviewerCodex || !got[0].Rerun || got[0].Source != domain.ReviewTriggerAgent {
		t.Fatalf("started = %+v, want one agent rerun of the queued PR", got)
	}
	if q, _ := st.ListQueuedReviewTriggers(ctx); len(q) != 0 {
		t.Fatalf("queue = %+v, want it cleared once started", q)
	}
}

func queuedFixture(t *testing.T, created time.Time) (*Service, domain.QueuedReviewTrigger) {
	t.Helper()
	st := sqlitetest.MustOpen(t)
	seedNoticeWorker(t, st, "w-1")
	q := domain.QueuedReviewTrigger{SessionID: "w-1", PRURL: "https://github.com/o/r/pull/7", Harness: domain.ReviewerCodex, CreatedAt: created}
	if err := st.EnqueueReviewTrigger(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	return New(reviewcore.New(reviewcore.Deps{Store: st}), st, WithClock(func() time.Time { return time.Unix(1000, 0).UTC() })), q
}

func queueLen(t *testing.T, s *Service) int {
	t.Helper()
	q, err := s.engine.QueuedTriggers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return len(q)
}

// The drain must keep the only copy of a pass unless it started or can never start.
func TestDrainKeepsQueuedPassUnlessItStartedOrCannotStart(t *testing.T) {
	fresh, old := time.Unix(900, 0).UTC(), time.Unix(1000, 0).UTC().Add(-48*time.Hour)
	for _, tc := range []struct {
		name    string
		created time.Time
		result  reviewcore.TriggerResult
		err     error
		want    int
	}{
		{"started", fresh, reviewcore.TriggerResult{Created: true}, nil, 0},
		{"reviewer got busy again", fresh, reviewcore.TriggerResult{Queued: true}, nil, 1},
		{"transient error", fresh, reviewcore.TriggerResult{}, errors.New("launch failed"), 1},
		{"install in progress", fresh, reviewcore.TriggerResult{}, reviewcore.ErrHarnessInstallActive, 1},
		{"permanent: invalid", fresh, reviewcore.TriggerResult{}, reviewcore.ErrInvalid, 0},
		{"permanent: not found", fresh, reviewcore.TriggerResult{}, reviewcore.ErrNotFound, 0},
		{"already running", fresh, reviewcore.TriggerResult{}, reviewcore.ErrReviewAlreadyRunning, 0},
		{"too old", old, reviewcore.TriggerResult{}, errors.New("launch failed"), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := queuedFixture(t, tc.created)
			svc.engineTrigger = func(context.Context, domain.SessionID, reviewcore.TriggerOptions) (reviewcore.TriggerResult, error) {
				return tc.result, tc.err
			}
			svc.drainQueuedTriggers(context.Background())
			if got := queueLen(t, svc); got != tc.want {
				t.Fatalf("queue length = %d, want %d", got, tc.want)
			}
		})
	}
}

// A hung launch is bounded and keeps its entry.
func TestDrainBoundsAHungLaunchAndKeepsTheEntry(t *testing.T) {
	svc, _ := queuedFixture(t, time.Unix(900, 0).UTC())
	svc.engineTrigger = func(ctx context.Context, _ domain.SessionID, _ reviewcore.TriggerOptions) (reviewcore.TriggerResult, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("queued trigger has no deadline")
		}
		return reviewcore.TriggerResult{}, context.DeadlineExceeded
	}
	svc.drainQueuedTriggers(context.Background())
	if got := queueLen(t, svc); got != 1 {
		t.Fatalf("queue length = %d, want the timed-out pass kept", got)
	}
}

// B394 restart: a pass cancelled or failed because its reviewer is gone is
// resumed by the one retry owner. A full Sweep must start exactly one pass,
// whichever path ended it, and leave the queue empty.
func TestSweepResumesAPassWhoseReviewerIsGoneExactlyOnce(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status domain.ReviewRunStatus
		body   string
	}{
		{"terminal unavailable (trigger-first)", domain.ReviewRunCancelled, "cancelled because reviewer terminal is unavailable"},
		{"chat unavailable (restore-first)", domain.ReviewRunCancelled, "cancelled because reviewer Chat is unavailable"},
		{"restore failed", domain.ReviewRunFailed, reviewcore.ReviewerRestoreFailedPrefix + "reviewer process ended and cannot be restored idle; retry the review"},
		{"process exited", domain.ReviewRunFailed, "reviewer process exited before submitting a result"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st := sqlitetest.MustOpen(t)
			seedNoticeWorker(t, st, "w-1")
			seedRetryPR(t, st, "sha-a")
			t0 := time.Unix(1000, 0).UTC()
			seedEndedRun(t, st, "run-1", "w-1", "sha-a", tc.status, tc.body, t0)
			svc := New(reviewcore.New(reviewcore.Deps{Store: st}), st, WithClock(func() time.Time { return t0.Add(time.Hour) }))
			svc.SetFailureNotice(st, &fakeFailureReports{}, &fakeWorkerNudger{})
			var got []reviewcore.TriggerOptions
			svc.engineTrigger = func(_ context.Context, _ domain.SessionID, o reviewcore.TriggerOptions) (reviewcore.TriggerResult, error) {
				got = append(got, o)
				return reviewcore.TriggerResult{Created: true, CreatedRuns: []domain.ReviewRun{{ID: "x"}}}, nil
			}
			if err := svc.Sweep(ctx); err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].RetryOfRunID != "run-1" {
				t.Fatalf("triggers = %+v, want exactly one retry of run-1", got)
			}
			if n := queueLen(t, svc); n != 0 {
				t.Fatalf("queue length = %d, want 0: a dead pass has one owner, the retry", n)
			}
		})
	}
}

// A pass that keeps failing to start backs off, stops at the attempt cap, and
// tells its worker; the drain never wakes the sweep that is running it.
func TestDrainBacksOffCapsAttemptsAndTellsTheWorker(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1000, 0).UTC()
	st := sqlitetest.MustOpen(t)
	seedNoticeWorker(t, st, "w-1")
	q := domain.QueuedReviewTrigger{SessionID: "w-1", PRURL: "https://github.com/o/r/pull/7", Harness: domain.ReviewerCodex, CreatedAt: now, Model: "m-1", Effort: "high", InterfaceMode: domain.ReviewerInterfaceTUI}
	if err := st.EnqueueReviewTrigger(ctx, q); err != nil {
		t.Fatal(err)
	}
	svc := New(reviewcore.New(reviewcore.Deps{Store: st}), st, WithClock(func() time.Time { return now }))
	nudger := &fakeWorkerNudger{}
	svc.SetFailureNotice(st, &fakeFailureReports{}, nudger)
	var calls []reviewcore.TriggerOptions
	svc.engineTrigger = func(_ context.Context, _ domain.SessionID, o reviewcore.TriggerOptions) (reviewcore.TriggerResult, error) {
		calls = append(calls, o)
		return reviewcore.TriggerResult{}, errors.New("spawn failed")
	}
	svc.drainQueuedTriggers(ctx)
	if len(calls) != 1 || calls[0].Config.Model != "m-1" || calls[0].Config.Effort != "high" || calls[0].InterfaceMode != domain.ReviewerInterfaceTUI || calls[0].QueuedSince != now {
		t.Fatalf("calls = %+v, want the queued options and QueuedSince passed through", calls)
	}
	select {
	case <-svc.sweepWake:
		t.Fatal("a drain attempt woke the sweep")
	default:
	}
	svc.drainQueuedTriggers(ctx) // still backing off
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want the retry held back by the backoff", len(calls))
	}
	for i := 2; i <= queuedTriggerMaxAttempts; i++ {
		now = now.Add(time.Hour)
		svc.drainQueuedTriggers(ctx)
	}
	if len(calls) != queuedTriggerMaxAttempts {
		t.Fatalf("calls = %d, want %d attempts", len(calls), queuedTriggerMaxAttempts)
	}
	if n := queueLen(t, svc); n != 0 {
		t.Fatalf("queue length = %d, want the entry dropped at the cap", n)
	}
	if len(nudger.messages) != 1 || !strings.Contains(nudger.messages[0], "never started") {
		t.Fatalf("worker messages = %q, want one dropped-pass notice", nudger.messages)
	}
}

// B394, two PRs on one worker: both passes died with the app. The retry owner
// starts the first; the second finds the reviewer busy and is parked, then
// starts once the reviewer is free, so neither is lost.
func TestSweepResumesTwoDeadPassesOnOneWorker(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.MustOpen(t)
	seedNoticeWorker(t, st, "w-1")
	t0 := time.Unix(1000, 0).UTC()
	prs := []string{"https://github.com/o/r/pull/7", "https://github.com/o/r/pull/8"}
	for i, url := range prs {
		pr := domain.PullRequest{URL: url, Number: 7 + i, Provider: "github", Repo: "o/r", HeadSHA: "sha-" + url[len(url)-1:], SessionID: "w-1"}
		if _, err := st.ClaimPR(ctx, pr, nil, nil, nil, nil, ports.ReviewWritePreserve, true); err != nil {
			t.Fatal(err)
		}
		run := domain.ReviewRun{ID: fmt.Sprintf("dead-%d", i), ReviewID: "rev-w-1", SessionID: "w-1", Harness: domain.ReviewerCodex, PRURL: url, TargetSHA: pr.HeadSHA, Status: domain.ReviewRunRunning, CreatedAt: t0.Add(time.Duration(i) * time.Second)}
		if err := st.InsertReviewRun(ctx, run); err != nil {
			t.Fatal(err)
		}
		if _, err := st.UpdateReviewRunResult(ctx, run.ID, domain.ReviewRunFailed, domain.VerdictNone, reviewcore.ReviewerInterruptedByRestart, "", false); err != nil {
			t.Fatal(err)
		}
	}
	clock := func() time.Time { return t0.Add(time.Hour) }
	svc := New(reviewcore.New(reviewcore.Deps{Store: st, Clock: clock}), st, WithClock(clock))
	svc.SetFailureNotice(st, &fakeFailureReports{}, &fakeWorkerNudger{})
	var started []string
	svc.engineTrigger = func(_ context.Context, _ domain.SessionID, o reviewcore.TriggerOptions) (reviewcore.TriggerResult, error) {
		if running, _ := st.ListRunningReviewRunsBySession(ctx, "w-1"); len(running) > 0 {
			return reviewcore.TriggerResult{}, nil // the reviewer is busy: nothing created
		}
		started = append(started, o.PRURL)
		run := domain.ReviewRun{ID: "live-" + o.PRURL[len(o.PRURL)-1:], ReviewID: "rev-w-1", SessionID: "w-1", Harness: domain.ReviewerCodex, PRURL: o.PRURL, TargetSHA: "x", Status: domain.ReviewRunRunning, CreatedAt: t0.Add(time.Hour)}
		if err := st.InsertReviewRun(ctx, run); err != nil {
			t.Fatal(err)
		}
		return reviewcore.TriggerResult{Created: true, CreatedRuns: []domain.ReviewRun{run}}, nil
	}
	if err := svc.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if len(started) != 1 || queueLen(t, svc) != 1 {
		t.Fatalf("after the first sweep started = %v queue = %d, want one started and the other parked", started, queueLen(t, svc))
	}
	if _, err := st.UpdateReviewRunResult(ctx, "live-"+started[0][len(started[0])-1:], domain.ReviewRunComplete, domain.VerdictApproved, "ok", "", false); err != nil {
		t.Fatal(err)
	}
	if err := svc.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if len(started) != 2 || started[0] == started[1] || queueLen(t, svc) != 0 {
		t.Fatalf("after the second sweep started = %v queue = %d, want both PRs started once each", started, queueLen(t, svc))
	}
}

// A parked automatic pass keeps its auto policy: it restarts as an auto pass
// (not a rerun), is dropped quietly when auto-review no longer applies, and is
// kept when the reviewer got busy again.
func TestParkedAutoPassKeepsItsAutoPolicy(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result reviewcore.TriggerResult
		want   int
	}{
		{"auto review disabled meanwhile", reviewcore.TriggerResult{SkipReason: "disabled"}, 0},
		{"worker terminated", reviewcore.TriggerResult{SkipReason: "terminated"}, 0},
		{"worker not idle yet", reviewcore.TriggerResult{SkipReason: "not_idle"}, 1},
		{"worker idle threshold not met", reviewcore.TriggerResult{SkipReason: "idle_threshold_not_met"}, 1},
		{"started", reviewcore.TriggerResult{Created: true}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, q := queuedFixture(t, time.Unix(900, 0).UTC())
			st := svc.store.(interface {
				EnqueueReviewTrigger(context.Context, domain.QueuedReviewTrigger) error
				DeleteQueuedReviewTrigger(context.Context, domain.QueuedReviewTrigger) error
			})
			if err := st.DeleteQueuedReviewTrigger(context.Background(), q); err != nil {
				t.Fatal(err)
			}
			q.TriggerSource = domain.ReviewTriggerAuto
			if err := st.EnqueueReviewTrigger(context.Background(), q); err != nil {
				t.Fatal(err)
			}
			var got []reviewcore.TriggerOptions
			svc.engineTrigger = func(_ context.Context, _ domain.SessionID, o reviewcore.TriggerOptions) (reviewcore.TriggerResult, error) {
				got = append(got, o)
				return tc.result, nil
			}
			nudger := &fakeWorkerNudger{}
			svc.SetFailureNotice(nil, nil, nudger)
			svc.drainQueuedTriggers(context.Background())
			if len(got) != 1 || got[0].Source != domain.ReviewTriggerAuto || got[0].Rerun {
				t.Fatalf("trigger = %+v, want one auto pass that is not a rerun", got)
			}
			if n := queueLen(t, svc); n != tc.want || len(nudger.messages) != 0 {
				t.Fatalf("queue length = %d (want %d), worker messages = %q: the entry must go quietly", n, tc.want, nudger.messages)
			}
		})
	}
}

// An agent's rerun takes over a parked automatic entry for the same PR: it must
// not be downgraded to the auto policy (which could drop it quietly), and it
// keeps its options.
func TestAgentRerunTakesOverAParkedAutoEntry(t *testing.T) {
	ctx := context.Background()
	svc, q := queuedFixture(t, time.Unix(900, 0).UTC())
	st := svc.store.(interface {
		EnqueueReviewTrigger(context.Context, domain.QueuedReviewTrigger) error
		DeleteQueuedReviewTrigger(context.Context, domain.QueuedReviewTrigger) error
	})
	if err := st.DeleteQueuedReviewTrigger(ctx, q); err != nil {
		t.Fatal(err)
	}
	auto := q
	auto.TriggerSource = domain.ReviewTriggerAuto
	if err := st.EnqueueReviewTrigger(ctx, auto); err != nil {
		t.Fatal(err)
	}
	agent := q
	agent.TriggerSource, agent.Model, agent.Effort, agent.CreatedAt = domain.ReviewTriggerAgent, "m-2", "high", time.Unix(950, 0).UTC()
	if err := st.EnqueueReviewTrigger(ctx, agent); err != nil {
		t.Fatal(err)
	}
	// A later auto park does not take the row back.
	if err := st.EnqueueReviewTrigger(ctx, auto); err != nil {
		t.Fatal(err)
	}
	var got []reviewcore.TriggerOptions
	svc.engineTrigger = func(_ context.Context, _ domain.SessionID, o reviewcore.TriggerOptions) (reviewcore.TriggerResult, error) {
		got = append(got, o)
		return reviewcore.TriggerResult{Created: true}, nil
	}
	svc.drainQueuedTriggers(ctx)
	if len(got) != 1 || got[0].Source != domain.ReviewTriggerAgent || !got[0].Rerun || got[0].Config.Model != "m-2" || got[0].Config.Effort != "high" {
		t.Fatalf("drained = %+v, want one agent Rerun pass with the agent's options", got)
	}
}
