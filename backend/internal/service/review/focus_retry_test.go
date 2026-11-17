package review

import (
	"context"
	"fmt"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	reviewcore "tallpine.eu/tallpine/backend/internal/review"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

const retryFocus = "check the migration first"

// B743: the one-shot retry of a pass that ended without a verdict reviews with
// the focus the dead pass was requested with.
func TestUnsubmittedReviewRetryKeepsFocus(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.MustOpen(t)
	seedNoticeWorker(t, st, "w-1")
	seedRetryPR(t, st, "sha-a")
	t0 := time.Unix(1000, 0).UTC()
	if err := st.InsertReviewRun(ctx, domain.ReviewRun{
		ID: "run-1", ReviewID: "rev-w-1", SessionID: "w-1", Harness: domain.ReviewerCodex, Model: "gpt-5.5",
		PRURL: "https://github.com/o/r/pull/7", TargetSHA: "sha-a", Status: domain.ReviewRunRunning, CreatedAt: t0,
		TriggerSource: domain.ReviewTriggerAgent, Focus: retryFocus, FocusAuthor: domain.ReviewFocusWorker,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdateReviewRunResult(ctx, "run-1", domain.ReviewRunFailed, domain.VerdictNone, "reviewer turn ended without submitting a result", "", false); err != nil {
		t.Fatal(err)
	}
	svc := New(nil, st, WithClock(func() time.Time { return t0.Add(time.Hour) }))
	svc.SetFailureNotice(st, &fakeFailureReports{}, &fakeWorkerNudger{})
	var triggers []reviewcore.TriggerOptions
	svc.engineTrigger = func(_ context.Context, _ domain.SessionID, o reviewcore.TriggerOptions) (reviewcore.TriggerResult, error) {
		triggers = append(triggers, o)
		return reviewcore.TriggerResult{Created: true, CreatedRuns: []domain.ReviewRun{{ID: "run-2"}}}, nil
	}
	if err := svc.NotifyEndedRuns(ctx); err != nil {
		t.Fatal(err)
	}
	if len(triggers) != 1 || triggers[0].RetryOfRunID != "run-1" || triggers[0].Focus != retryFocus || triggers[0].FocusAuthor != domain.ReviewFocusWorker {
		t.Fatalf("triggers = %+v, want one retry with the dead pass's focus and author", triggers)
	}
}

// B743: a retry parked behind a busy reviewer keeps the focus in the queue, and
// the drain starts it with that focus.
func TestParkedRetryKeepsFocusThroughTheQueue(t *testing.T) {
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
		run := domain.ReviewRun{ID: fmt.Sprintf("dead-%d", i), ReviewID: "rev-w-1", SessionID: "w-1", Harness: domain.ReviewerCodex, PRURL: url, TargetSHA: pr.HeadSHA, Status: domain.ReviewRunRunning, CreatedAt: t0.Add(time.Duration(i) * time.Second),
			TriggerSource: domain.ReviewTriggerAgent, Focus: retryFocus + url, FocusAuthor: domain.ReviewFocusOrchestrator}
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
	var started []reviewcore.TriggerOptions
	svc.engineTrigger = func(_ context.Context, _ domain.SessionID, o reviewcore.TriggerOptions) (reviewcore.TriggerResult, error) {
		if running, _ := st.ListRunningReviewRunsBySession(ctx, "w-1"); len(running) > 0 {
			return reviewcore.TriggerResult{}, nil // the reviewer is busy: nothing created
		}
		started = append(started, o)
		run := domain.ReviewRun{ID: "live-" + o.PRURL[len(o.PRURL)-1:], ReviewID: "rev-w-1", SessionID: "w-1", Harness: domain.ReviewerCodex, PRURL: o.PRURL, TargetSHA: "x", Status: domain.ReviewRunRunning, CreatedAt: t0.Add(time.Hour)}
		if err := st.InsertReviewRun(ctx, run); err != nil {
			t.Fatal(err)
		}
		return reviewcore.TriggerResult{Created: true, CreatedRuns: []domain.ReviewRun{run}}, nil
	}
	if err := svc.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	queued, err := svc.engine.QueuedTriggers(ctx)
	if err != nil || len(started) != 1 || len(queued) != 1 {
		t.Fatalf("after the first sweep started = %+v queued = %+v err = %v, want one started and one parked", started, queued, err)
	}
	if queued[0].Focus != retryFocus+queued[0].PRURL || queued[0].FocusAuthor != domain.ReviewFocusOrchestrator {
		t.Fatalf("parked entry = %+v, want its dead pass's focus", queued[0])
	}
	if _, err := st.UpdateReviewRunResult(ctx, "live-"+started[0].PRURL[len(started[0].PRURL)-1:], domain.ReviewRunComplete, domain.VerdictApproved, "ok", "", false); err != nil {
		t.Fatal(err)
	}
	if err := svc.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if len(started) != 2 {
		t.Fatalf("started = %+v, want the parked pass started", started)
	}
	for _, o := range started {
		if o.Focus != retryFocus+o.PRURL || o.FocusAuthor != domain.ReviewFocusOrchestrator {
			t.Fatalf("started %s with focus %q/%q, want its own dead pass's focus", o.PRURL, o.Focus, o.FocusAuthor)
		}
	}
}

// B743: a pass restarted by a focused trigger (the engine records the focus on
// the restarted row) that then ends unsubmitted is retried with that focus.
func TestRestartedPassRetryKeepsTheRestartFocus(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.MustOpen(t)
	seedNoticeWorker(t, st, "w-1")
	seedRetryPR(t, st, "sha-a")
	t0 := time.Unix(1000, 0).UTC()
	seedEndedRun(t, st, "run-1", "w-1", "sha-a", domain.ReviewRunRunning, "", t0)
	if err := st.SetReviewRunFocus(ctx, "run-1", retryFocus, domain.ReviewFocusOwner); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdateReviewRunResult(ctx, "run-1", domain.ReviewRunFailed, domain.VerdictNone, "reviewer turn ended without submitting a result", "", false); err != nil {
		t.Fatal(err)
	}
	svc := New(nil, st, WithClock(func() time.Time { return t0.Add(time.Hour) }))
	svc.SetFailureNotice(st, &fakeFailureReports{}, &fakeWorkerNudger{})
	var triggers []reviewcore.TriggerOptions
	svc.engineTrigger = func(_ context.Context, _ domain.SessionID, o reviewcore.TriggerOptions) (reviewcore.TriggerResult, error) {
		triggers = append(triggers, o)
		return reviewcore.TriggerResult{Created: true, CreatedRuns: []domain.ReviewRun{{ID: "run-2"}}}, nil
	}
	if err := svc.NotifyEndedRuns(ctx); err != nil {
		t.Fatal(err)
	}
	if len(triggers) != 1 || triggers[0].Focus != retryFocus || triggers[0].FocusAuthor != domain.ReviewFocusOwner {
		t.Fatalf("triggers = %+v, want the retry to carry the restart's focus", triggers)
	}
}
