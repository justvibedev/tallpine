package review

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	reviewcore "tallpine.eu/tallpine/backend/internal/review"
	reportsvc "tallpine.eu/tallpine/backend/internal/service/report"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

type fakeFailureReports struct {
	inputs []reportsvc.CreateInput
	// fail makes the next fail calls return an error without filing.
	fail int
}

func (f *fakeFailureReports) Create(_ context.Context, in reportsvc.CreateInput) (domain.ReportRecord, error) {
	if f.fail > 0 {
		f.fail--
		return domain.ReportRecord{}, errors.New("database is locked")
	}
	f.inputs = append(f.inputs, in)
	return domain.ReportRecord{SessionID: in.SessionID, Message: in.Message}, nil
}

type fakeWorkerNudger struct {
	ids      []domain.SessionID
	messages []string
}

func (f *fakeWorkerNudger) Nudge(_ context.Context, id domain.SessionID, message string) (bool, error) {
	f.ids = append(f.ids, id)
	f.messages = append(f.messages, message)
	return true, nil
}

func seedNoticeWorker(t *testing.T, st *sqlite.Store, id domain.SessionID) {
	t.Helper()
	ctx := context.Background()
	// Session ids are allocated as <project>-<n>, so project "w" yields "w-1".
	if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: "w", Path: "/tmp/w", RegisteredAt: time.Now().UTC().Truncate(time.Second)}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if rec, err := st.CreateSession(ctx, domain.SessionRecord{ProjectID: "w", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode}); err != nil || rec.ID != id {
		t.Fatalf("seed worker: id=%q err=%v, want %q", rec.ID, err, id)
	}
	if err := st.UpsertReview(ctx, domain.Review{ID: "rev-" + string(id), SessionID: id, ProjectID: "w", Harness: domain.ReviewerCodex, CreatedAt: time.Unix(0, 0).UTC(), UpdatedAt: time.Unix(0, 0).UTC()}); err != nil {
		t.Fatalf("seed review: %v", err)
	}
}

// seedEndedRun records a pass and ends it the way the engine does.
func seedEndedRun(t *testing.T, st *sqlite.Store, id string, worker domain.SessionID, sha string, status domain.ReviewRunStatus, body string, at time.Time) {
	t.Helper()
	ctx := context.Background()
	if err := st.InsertReviewRun(ctx, domain.ReviewRun{
		ID: id, ReviewID: "rev-" + string(worker), SessionID: worker, Harness: domain.ReviewerCodex, Model: "gpt-5.5",
		PRURL: "https://github.com/o/r/pull/7", TargetSHA: sha, Status: domain.ReviewRunRunning, CreatedAt: at,
	}); err != nil {
		t.Fatalf("insert run %s: %v", id, err)
	}
	if status == domain.ReviewRunRunning {
		return
	}
	if ok, err := st.UpdateReviewRunResult(ctx, id, status, domain.VerdictNone, body, "", false); err != nil || !ok {
		t.Fatalf("end run %s: ok=%v err=%v", id, ok, err)
	}
}

func TestNotifyEndedRunsTellsWorkerAndOrchestratorOncePerRun(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.MustOpen(t)
	seedNoticeWorker(t, st, "w-1")
	t0 := time.Unix(1000, 0).UTC()
	seedEndedRun(t, st, "run-failed", "w-1", "sha-a", domain.ReviewRunFailed, "reviewer process exited before submitting a result", t0)
	seedEndedRun(t, st, "run-user", "w-1", "sha-b", domain.ReviewRunCancelled, "cancelled by user", t0.Add(time.Second))
	seedEndedRun(t, st, "run-superseded", "w-1", "sha-c", domain.ReviewRunFailed, "superseded by a review trigger for a newer commit", t0.Add(2*time.Second))
	seedEndedRun(t, st, "run-unavailable", "w-1", "sha-d", domain.ReviewRunCancelled, "cancelled because reviewer terminal is unavailable", t0.Add(3*time.Second))
	seedEndedRun(t, st, "run-running", "w-1", "sha-e", domain.ReviewRunRunning, "", t0.Add(4*time.Second))

	reports, nudger := &fakeFailureReports{}, &fakeWorkerNudger{}
	svc := New(nil, st, WithClock(func() time.Time { return t0.Add(time.Hour) }))
	svc.SetFailureNotice(st, reports, nudger)

	if err := svc.NotifyEndedRuns(ctx); err != nil {
		t.Fatalf("NotifyEndedRuns: %v", err)
	}
	if len(reports.inputs) != 2 || len(nudger.messages) != 2 {
		t.Fatalf("reports=%+v nudges=%q, want the failed and the system-cancelled pass only", reports.inputs, nudger.messages)
	}
	first := reports.inputs[0]
	if first.SessionID != "w-1" || !first.Immediate || !first.Synthesized {
		t.Fatalf("report = %+v, want an immediate synthesized report on the worker", first)
	}
	for _, want := range []string{"[auto] review_failed", "head sha-a", "by codex (gpt-5.5) failed", "reviewer process exited before submitting a result", "tallpine review ls w-1"} {
		if !strings.Contains(first.Message, want) {
			t.Fatalf("report message %q, want %q", first.Message, want)
		}
	}
	if !strings.Contains(reports.inputs[1].Message, "was cancelled: cancelled because reviewer terminal is unavailable") {
		t.Fatalf("report message %q, want the cancellation reason", reports.inputs[1].Message)
	}
	if nudger.ids[0] != "w-1" || !strings.Contains(nudger.messages[0], "failed without a verdict: reviewer process exited before submitting a result") {
		t.Fatalf("nudge = %q to %v, want the failure reason for the worker", nudger.messages[0], nudger.ids)
	}

	// A re-list or the next sweep never repeats a notice.
	if err := svc.NotifyEndedRuns(ctx); err != nil {
		t.Fatalf("second NotifyEndedRuns: %v", err)
	}
	if len(reports.inputs) != 2 || len(nudger.messages) != 2 {
		t.Fatalf("notices repeated: reports=%d nudges=%d", len(reports.inputs), len(nudger.messages))
	}
	if left, err := st.ListUnnotifiedEndedReviewRuns(ctx, 10); err != nil || len(left) != 0 {
		t.Fatalf("unnotified after sweep = %+v err=%v, want every ended pass claimed", left, err)
	}

	// The running pass is announced once it ends.
	if ok, err := st.UpdateReviewRunResult(ctx, "run-running", domain.ReviewRunFailed, domain.VerdictNone, "reviewer did not submit a result within the 45m0s review deadline", "", false); err != nil || !ok {
		t.Fatalf("fail running pass: ok=%v err=%v", ok, err)
	}
	if err := svc.NotifyEndedRuns(ctx); err != nil {
		t.Fatalf("third NotifyEndedRuns: %v", err)
	}
	if len(reports.inputs) != 3 || !strings.Contains(reports.inputs[2].Message, "review deadline") {
		t.Fatalf("reports = %+v, want the deadline failure announced", reports.inputs)
	}
}

func TestNotifyEndedRunsSkipsTerminatedWorker(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.MustOpen(t)
	seedNoticeWorker(t, st, "w-1")
	rec, _, err := st.GetSession(ctx, "w-1")
	if err != nil {
		t.Fatalf("get worker: %v", err)
	}
	rec.IsTerminated = true
	if err := st.UpdateSession(ctx, rec); err != nil {
		t.Fatalf("terminate worker: %v", err)
	}
	seedEndedRun(t, st, "run-failed", "w-1", "sha-a", domain.ReviewRunFailed, "launch failed", time.Unix(1000, 0).UTC())

	reports, nudger := &fakeFailureReports{}, &fakeWorkerNudger{}
	svc := New(nil, st)
	svc.SetFailureNotice(st, reports, nudger)
	if err := svc.NotifyEndedRuns(ctx); err != nil {
		t.Fatalf("NotifyEndedRuns: %v", err)
	}
	if len(reports.inputs) != 0 || len(nudger.messages) != 0 {
		t.Fatalf("reports=%+v nudges=%q, want nothing for a terminated worker", reports.inputs, nudger.messages)
	}
}

func TestFailureNoticeClaimIsOncePerRun(t *testing.T) {
	// A pass ended after the migration is unclaimed, keeps its model, and can
	// be claimed exactly once.
	ctx := context.Background()
	st := sqlitetest.MustOpen(t)
	seedNoticeWorker(t, st, "w-1")
	seedEndedRun(t, st, "run-new", "w-1", "sha-a", domain.ReviewRunFailed, "launch failed", time.Unix(1000, 0).UTC())
	runs, err := st.ListUnnotifiedEndedReviewRuns(ctx, 10)
	if err != nil || len(runs) != 1 || runs[0].ID != "run-new" || runs[0].Model != "gpt-5.5" {
		t.Fatalf("unnotified = %+v err=%v, want the new failed run with its model", runs, err)
	}
	if ok, err := st.MarkReviewRunFailureNotified(ctx, "run-new", time.Unix(2000, 0).UTC()); err != nil || !ok {
		t.Fatalf("first claim ok=%v err=%v", ok, err)
	}
	if ok, err := st.MarkReviewRunFailureNotified(ctx, "run-new", time.Unix(2001, 0).UTC()); err != nil || ok {
		t.Fatalf("second claim ok=%v err=%v, want already claimed", ok, err)
	}
}

func TestNotifyEndedRunsRetriesAReportThatFailedToFile(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.MustOpen(t)
	seedNoticeWorker(t, st, "w-1")
	t0 := time.Unix(1000, 0).UTC()
	seedEndedRun(t, st, "run-failed", "w-1", "sha-a", domain.ReviewRunFailed, "reviewer process exited before submitting a result", t0)

	reports, nudger := &fakeFailureReports{fail: 1}, &fakeWorkerNudger{}
	svc := New(nil, st, WithClock(func() time.Time { return t0.Add(time.Hour) }))
	svc.SetFailureNotice(st, reports, nudger)

	if err := svc.NotifyEndedRuns(ctx); err != nil {
		t.Fatalf("first NotifyEndedRuns: %v", err)
	}
	if len(reports.inputs) != 0 || len(nudger.messages) != 0 {
		t.Fatalf("reports=%d nudges=%d after a failed report, want nothing sent yet", len(reports.inputs), len(nudger.messages))
	}
	if left, err := st.ListUnnotifiedEndedReviewRuns(ctx, 10); err != nil || len(left) != 1 {
		t.Fatalf("unnotified = %+v err=%v, want the run kept for retry", left, err)
	}

	for i := 0; i < 3; i++ {
		if err := svc.NotifyEndedRuns(ctx); err != nil {
			t.Fatalf("retry %d: %v", i, err)
		}
	}
	if len(reports.inputs) != 1 || len(nudger.messages) != 1 {
		t.Fatalf("reports=%d nudges=%d after retries, want exactly one of each", len(reports.inputs), len(nudger.messages))
	}
	if reports.inputs[0].ID != "rpt_review_failed_run-failed" {
		t.Fatalf("report id = %q, want the run's stable id", reports.inputs[0].ID)
	}
}

// A daemon that stops after filing the report but before claiming the run
// files nothing twice on the next sweep: the report ID is stable.
func TestNotifyEndedRunsDoesNotDuplicateAReportFiledBeforeTheClaim(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.MustOpen(t)
	seedNoticeWorker(t, st, "w-1")
	t0 := time.Unix(1000, 0).UTC()
	seedEndedRun(t, st, "run-failed", "w-1", "sha-a", domain.ReviewRunFailed, "launch failed", t0)
	reports := reportsvc.New(reportsvc.Deps{Store: st})
	if _, err := reports.Create(ctx, reportsvc.CreateInput{ID: failureReportID("run-failed"), SessionID: "w-1", Message: "[auto] review_failed: earlier attempt", Immediate: true, Synthesized: true}); err != nil {
		t.Fatalf("pre-file report: %v", err)
	}

	nudger := &fakeWorkerNudger{}
	svc := New(nil, st, WithClock(func() time.Time { return t0.Add(time.Hour) }))
	svc.SetFailureNotice(st, reports, nudger)
	for i := 0; i < 2; i++ {
		if err := svc.NotifyEndedRuns(ctx); err != nil {
			t.Fatalf("NotifyEndedRuns %d: %v", i, err)
		}
	}
	filed, err := st.ListReportsByProject(ctx, "w")
	if err != nil || len(filed) != 1 {
		t.Fatalf("reports = %+v err=%v, want the one report, not a duplicate", filed, err)
	}
	if len(nudger.messages) != 1 {
		t.Fatalf("nudges = %q, want one once the run is claimed", nudger.messages)
	}
	if left, err := st.ListUnnotifiedEndedReviewRuns(ctx, 10); err != nil || len(left) != 0 {
		t.Fatalf("unnotified = %+v err=%v, want the run claimed", left, err)
	}
}

func TestNotifyEndedRunsGivesUpOnAReportThatNeverFiles(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.MustOpen(t)
	seedNoticeWorker(t, st, "w-1")
	t0 := time.Unix(1000, 0).UTC()
	seedEndedRun(t, st, "run-failed", "w-1", "sha-a", domain.ReviewRunFailed, "launch failed", t0)

	reports, nudger := &fakeFailureReports{fail: 1000}, &fakeWorkerNudger{}
	now := t0.Add(time.Hour)
	svc := New(nil, st, WithClock(func() time.Time { return now }))
	svc.SetFailureNotice(st, reports, nudger)
	if err := svc.NotifyEndedRuns(ctx); err != nil {
		t.Fatalf("NotifyEndedRuns: %v", err)
	}
	if len(nudger.messages) != 0 {
		t.Fatalf("nudged inside the retry window: %q", nudger.messages)
	}
	now = t0.Add(endedRunNoticeRetryWindow)
	if err := svc.NotifyEndedRuns(ctx); err != nil {
		t.Fatalf("NotifyEndedRuns after the window: %v", err)
	}
	if left, err := st.ListUnnotifiedEndedReviewRuns(ctx, 10); err != nil || len(left) != 0 {
		t.Fatalf("unnotified = %+v err=%v, want the run claimed once Tallpine gives up", left, err)
	}
	if len(nudger.messages) != 1 {
		t.Fatalf("nudges = %q, want the worker still told", nudger.messages)
	}
}

func TestApprovedReviewFilesDurableReportAcrossRetryAndRestart(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.MustOpen(t)
	seedNoticeWorker(t, st, "w-1")
	seedEndedRun(t, st, "approved", "w-1", "abc123", domain.ReviewRunRunning, "", time.Now().UTC())
	reports := reportsvc.New(reportsvc.Deps{Store: st})
	nudger := &fakeWorkerNudger{}
	svc := New(nil, st)
	svc.SetFailureNotice(st, reports, nudger)
	for range 2 {
		if _, err := svc.Submit(ctx, "w-1", "approved", domain.VerdictApproved, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-svc.sweepWake:
	default:
		t.Fatal("submit did not wake notice sweep")
	}
	// Crash after creating the report but before marking the notice handled.
	run, _, err := st.GetReviewRun(ctx, "approved")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.fileApprovalReport(ctx, svc.failureNotice(), run); err != nil {
		t.Fatal(err)
	}
	// Recreate the service and recover the same stable report ID from the store.
	svc = New(nil, st)
	svc.SetFailureNotice(st, reports, nudger)
	if err := svc.NotifyEndedRuns(ctx); err != nil {
		t.Fatal(err)
	}
	if err := svc.NotifyEndedRuns(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := st.ListReportsBySession(ctx, "w-1")
	if err != nil || len(got) != 1 {
		t.Fatalf("reports=%+v err=%v", got, err)
	}
	if got[0].ID != "rpt_review_approved_approved" || !strings.Contains(got[0].Message, "abc123") || !strings.Contains(got[0].Message, "not an independent GitHub approval") {
		t.Fatal(got[0])
	}
	if len(nudger.messages) != 0 {
		t.Fatal("duplicated feedback to writer")
	}
}

type recordingDelivery struct{ messages []string }

func (d *recordingDelivery) Submit(_ context.Context, _ domain.SessionID, message, _ string) error {
	d.messages = append(d.messages, message)
	return nil
}
func (d *recordingDelivery) Interrupt(context.Context, domain.SessionID) error { return nil }

type quietFixture struct {
	st       *sqlite.Store
	reports  *reportsvc.Service
	coord    *reportsvc.Coordinator
	delivery *recordingDelivery
	now      *time.Time
}

func newQuietFixture(t *testing.T) quietFixture {
	t.Helper()
	st := sqlitetest.MustOpen(t)
	seedNoticeWorker(t, st, "w-1")
	if _, err := st.CreateSession(context.Background(), domain.SessionRecord{ProjectID: "w", Kind: domain.KindOrchestrator, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	delivery := &recordingDelivery{}
	return quietFixture{
		st: st, now: &now, delivery: delivery,
		reports: reportsvc.New(reportsvc.Deps{Store: st, Now: func() time.Time { return now }}),
		coord:   reportsvc.NewCoordinator(reportsvc.CoordinatorDeps{Store: st, Delivery: delivery, Now: func() time.Time { return now }}),
	}
}

func (f quietFixture) file(t *testing.T, id, msg string, quiet bool) {
	t.Helper()
	if _, err := f.reports.Create(context.Background(), reportsvc.CreateInput{ID: id, SessionID: "w-1", Message: msg, Synthesized: true, Quiet: quiet}); err != nil {
		t.Fatal(err)
	}
}

func (f quietFixture) run(t *testing.T) {
	t.Helper()
	if _, err := f.coord.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestApprovalWakesOrchestratorImmediately(t *testing.T) {
	ctx := context.Background()
	f := newQuietFixture(t)
	seedEndedRun(t, f.st, "approved", "w-1", "abc123", domain.ReviewRunRunning, "", *f.now)
	svc := New(nil, f.st)
	svc.SetFailureNotice(f.st, f.reports, &fakeWorkerNudger{})
	if _, err := svc.Submit(ctx, "w-1", "approved", domain.VerdictApproved, "", ""); err != nil {
		t.Fatal(err)
	}
	run, _, _ := f.st.GetReviewRun(ctx, "approved")
	if err := svc.fileApprovalReport(ctx, svc.failureNotice(), run); err != nil {
		t.Fatal(err)
	}
	f.run(t)
	if len(f.delivery.messages) != 1 || !strings.Contains(f.delivery.messages[0], "review_approved") {
		t.Fatalf("approval did not wake the orchestrator: %q", f.delivery.messages)
	}
}

func TestQuietNoticeIsDeliveredByItsDeadlineWithNoOtherTraffic(t *testing.T) {
	f := newQuietFixture(t)
	f.file(t, "q1", "[auto] fyi quiet", true)
	*f.now = f.now.Add(reportsvc.DefaultQuietDelay - time.Minute)
	f.run(t)
	if len(f.delivery.messages) != 0 {
		t.Fatalf("quiet notice woke early: %q", f.delivery.messages)
	}
	*f.now = f.now.Add(2 * time.Minute)
	f.run(t)
	if len(f.delivery.messages) != 1 || !strings.Contains(f.delivery.messages[0], "fyi quiet") {
		t.Fatalf("quiet notice not delivered by its deadline: %q", f.delivery.messages)
	}
}

func TestQuietNoticePiggybacksOnNextActionableDelivery(t *testing.T) {
	f := newQuietFixture(t)
	f.file(t, "q1", "[auto] fyi quiet", true)
	f.file(t, "a1", "[auto] review_failed: x", false)
	f.run(t)
	if len(f.delivery.messages) != 1 || !strings.Contains(f.delivery.messages[0], "review_failed") || !strings.Contains(f.delivery.messages[0], "fyi quiet") {
		t.Fatalf("want one delivery carrying both: %q", f.delivery.messages)
	}
}

func TestQuietNoticeRidesOrchestratorsNextChatTurn(t *testing.T) {
	ctx := context.Background()
	f := newQuietFixture(t)
	f.file(t, "q1", "[auto] fyi quiet", true)
	batch, err := f.coord.PreparePiggyback(ctx, "w-2")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(batch.AppendToUserMessage("hi"), "fyi quiet") {
		t.Fatalf("quiet notice missing from next turn: %+v", batch.Reports)
	}
}

func TestRestartKeepsQuietNoticeQuietButBounded(t *testing.T) {
	ctx := context.Background()
	f := newQuietFixture(t)
	f.file(t, "q1", "[auto] fyi quiet", true)
	if _, err := f.st.RequeueClaimedReports(ctx); err != nil {
		t.Fatal(err)
	}
	f.run(t)
	if len(f.delivery.messages) != 0 {
		t.Fatalf("restart un-quieted the notice: %q", f.delivery.messages)
	}
	*f.now = f.now.Add(reportsvc.DefaultQuietDelay + time.Second)
	f.run(t)
	if len(f.delivery.messages) != 1 {
		t.Fatalf("quiet notice not bounded after restart: %q", f.delivery.messages)
	}
}

func TestRepeatApprovalForReportedHeadIsQuiet(t *testing.T) {
	ctx := context.Background()
	f := newQuietFixture(t)
	if err := f.st.UpsertReview(ctx, domain.Review{ID: "rev-2", SessionID: "w-1", ProjectID: "w", Harness: domain.ReviewerClaudeCode, CreatedAt: time.Unix(0, 0).UTC(), UpdatedAt: time.Unix(0, 0).UTC()}); err != nil {
		t.Fatal(err)
	}
	seedEndedRun(t, f.st, "first", "w-1", "abc123", domain.ReviewRunRunning, "", *f.now)
	if err := f.st.InsertReviewRun(ctx, domain.ReviewRun{ID: "second", ReviewID: "rev-2", SessionID: "w-1", Harness: domain.ReviewerClaudeCode,
		PRURL: "https://github.com/o/r/pull/7", TargetSHA: "abc123", Status: domain.ReviewRunRunning, CreatedAt: f.now.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	svc := New(nil, f.st)
	svc.SetFailureNotice(f.st, f.reports, &fakeWorkerNudger{})
	// Submit files the verdict's report itself (B426); the first wakes before
	// the repeat is filed.
	for _, id := range []string{"first", "second"} {
		if _, err := svc.Submit(ctx, "w-1", id, domain.VerdictApproved, "", ""); err != nil {
			t.Fatal(err)
		}
		f.run(t)
	}
	if len(f.delivery.messages) != 1 || strings.Count(f.delivery.messages[0], "review_approved") != 1 {
		t.Fatalf("want only the first approval to wake: %q", f.delivery.messages)
	}
	*f.now = f.now.Add(reportsvc.DefaultQuietDelay + time.Minute)
	f.run(t)
	if len(f.delivery.messages) != 2 {
		t.Fatalf("duplicate approval not delivered by its deadline: %q", f.delivery.messages)
	}
}

func TestReviewStartNoticesTheWorkerOncePerCreatedRun(t *testing.T) {
	svc := New(nil, &fakeStore{})
	nudger := &fakeWorkerNudger{}
	svc.SetFailureNotice(nil, nil, nudger)
	svc.engineTrigger = func(context.Context, domain.SessionID, reviewcore.TriggerOptions) (reviewcore.TriggerResult, error) {
		return reviewcore.TriggerResult{Created: true, CreatedRuns: []domain.ReviewRun{{ID: "r1", TargetSHA: "0123456789abcdef", AutoInjectReview: true}, {ID: "r2", TargetSHA: "0123456789abcdef"}}}, nil
	}
	if _, err := svc.TriggerAuto(context.Background(), "w-1", ""); err != nil {
		t.Fatal(err)
	}
	if len(nudger.messages) != 1 || nudger.ids[0] != "w-1" || !strings.Contains(nudger.messages[0], "Review running on 012345678") || !strings.Contains(nudger.messages[0], "Don't push") {
		t.Fatalf("nudges = %q", nudger.messages)
	}
	// A reused pass starts nothing and says nothing.
	svc.engineTrigger = func(context.Context, domain.SessionID, reviewcore.TriggerOptions) (reviewcore.TriggerResult, error) {
		return reviewcore.TriggerResult{Run: domain.ReviewRun{ID: "r1"}}, nil
	}
	if _, err := svc.TriggerAuto(context.Background(), "w-1", ""); err != nil {
		t.Fatal(err)
	}
	if len(nudger.messages) != 1 {
		t.Fatalf("a reused run was announced again: %q", nudger.messages)
	}
}

func TestFailureSubjectUsesProductName(t *testing.T) {
	got := failureSubject(domain.ReviewRun{}, 12)
	if !strings.HasPrefix(got, "Tallpine review of PR #12") {
		t.Fatalf("failureSubject = %q, want Tallpine product name", got)
	}
}

func seedRetryPR(t *testing.T, st *sqlite.Store, sha string) {
	t.Helper()
	pr := domain.PullRequest{URL: "https://github.com/o/r/pull/7", Number: 7, Provider: "github", Repo: "o/r", HeadSHA: sha, SessionID: "w-1"}
	if _, err := st.ClaimPR(context.Background(), pr, nil, nil, nil, nil, ports.ReviewWritePreserve, true); err != nil {
		t.Fatalf("seed pr: %v", err)
	}
}

func TestUnsubmittedReviewFailureRetriesOnceThenNotifies(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.MustOpen(t)
	seedNoticeWorker(t, st, "w-1")
	seedRetryPR(t, st, "sha-a")
	t0 := time.Unix(1000, 0).UTC()
	seedEndedRun(t, st, "run-1", "w-1", "sha-a", domain.ReviewRunFailed, "reviewer turn ended without submitting a result", t0)

	reports, nudger := &fakeFailureReports{}, &fakeWorkerNudger{}
	svc := New(nil, st, WithClock(func() time.Time { return t0.Add(time.Hour) }))
	svc.SetFailureNotice(st, reports, nudger)
	var triggers []reviewcore.TriggerOptions
	svc.engineTrigger = func(_ context.Context, _ domain.SessionID, o reviewcore.TriggerOptions) (reviewcore.TriggerResult, error) {
		triggers = append(triggers, o)
		seedEndedRun(t, st, "run-2", "w-1", "sha-a", domain.ReviewRunFailed, "reviewer turn ended without submitting a result", t0.Add(time.Minute))
		return reviewcore.TriggerResult{Created: true, CreatedRuns: []domain.ReviewRun{{ID: "run-2"}}}, nil
	}
	if err := svc.NotifyEndedRuns(ctx); err != nil {
		t.Fatal(err)
	}
	if len(triggers) != 1 || triggers[0].Harness != domain.ReviewerCodex || triggers[0].Config.Model != "gpt-5.5" || triggers[0].PRURL != "https://github.com/o/r/pull/7" || triggers[0].RetryOfRunID != "run-1" {
		t.Fatalf("triggers = %+v, want one retry with the same reviewer and model", triggers)
	}
	// The retry failed too: it is announced, and not retried again.
	if err := svc.NotifyEndedRuns(ctx); err != nil {
		t.Fatal(err)
	}
	if len(triggers) != 1 || len(reports.inputs) != 1 || len(nudger.messages) != 1 {
		t.Fatalf("triggers=%d reports=%+v nudges=%q, want exactly one notice for the failed retry", len(triggers), reports.inputs, nudger.messages)
	}
}

func TestUnsubmittedReviewFailureStillReportedWhenRetrySkipped(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.MustOpen(t)
	seedNoticeWorker(t, st, "w-1")
	seedRetryPR(t, st, "sha-a")
	t0 := time.Unix(1000, 0).UTC()
	seedEndedRun(t, st, "run-1", "w-1", "sha-a", domain.ReviewRunFailed, "reviewer process exited before submitting a result", t0)
	reports, nudger := &fakeFailureReports{}, &fakeWorkerNudger{}
	svc := New(nil, st, WithClock(func() time.Time { return t0.Add(time.Hour) }))
	svc.SetFailureNotice(st, reports, nudger)
	svc.engineTrigger = func(context.Context, domain.SessionID, reviewcore.TriggerOptions) (reviewcore.TriggerResult, error) {
		return reviewcore.TriggerResult{SkipReason: "worker is not idle"}, nil
	}
	if err := svc.NotifyEndedRuns(ctx); err != nil {
		t.Fatal(err)
	}
	if len(reports.inputs) != 1 || len(nudger.messages) != 1 {
		t.Fatalf("reports=%d nudges=%d, want the original failure announced", len(reports.inputs), len(nudger.messages))
	}
}

func TestUsageLimitedReviewRetriesAfterResetWithoutFailureReport(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.MustOpen(t)
	seedNoticeWorker(t, st, "w-1")
	seedRetryPR(t, st, "sha-a")
	t0 := time.Unix(1000, 0).UTC()
	body := reviewcore.ReviewerUsageLimitPrefix + "You've hit your 5-hour limit · resets " + t0.Add(2*time.Hour).Format(time.RFC3339)
	seedEndedRun(t, st, "run-1", "w-1", "sha-a", domain.ReviewRunFailed, body, t0)

	now := t0.Add(time.Hour)
	reports, nudger := &fakeFailureReports{}, &fakeWorkerNudger{}
	svc := New(nil, st, WithClock(func() time.Time { return now }))
	svc.SetFailureNotice(st, reports, nudger)
	triggers := 0
	svc.engineTrigger = func(context.Context, domain.SessionID, reviewcore.TriggerOptions) (reviewcore.TriggerResult, error) {
		triggers++
		return reviewcore.TriggerResult{Created: true, CreatedRuns: []domain.ReviewRun{{ID: "run-2"}}}, nil
	}
	if err := svc.NotifyEndedRuns(ctx); err != nil {
		t.Fatal(err)
	}
	if triggers != 0 || len(reports.inputs) != 0 || len(nudger.messages) != 0 {
		t.Fatalf("before reset: triggers=%d reports=%d nudges=%d, want all held", triggers, len(reports.inputs), len(nudger.messages))
	}
	now = t0.Add(3 * time.Hour)
	if err := svc.NotifyEndedRuns(ctx); err != nil {
		t.Fatal(err)
	}
	if triggers != 1 || len(reports.inputs) != 0 {
		t.Fatalf("after reset: triggers=%d reports=%d, want one retry and no failure report", triggers, len(reports.inputs))
	}
}

type prsStore struct {
	Store
	prs []domain.PullRequest
}

func (s prsStore) ListPRsBySession(context.Context, domain.SessionID) ([]domain.PullRequest, error) {
	return s.prs, nil
}

func TestApprovalReportSaysWhenHeadMoved(t *testing.T) {
	for _, tc := range []struct {
		name, head string
		wantNote   bool
	}{{"moved", "newsha", true}, {"same", "abc123", false}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st := sqlitetest.MustOpen(t)
			seedNoticeWorker(t, st, "w-1")
			seedEndedRun(t, st, "approved", "w-1", "abc123", domain.ReviewRunRunning, "", time.Now().UTC())
			reports := reportsvc.New(reportsvc.Deps{Store: st})
			svc := New(nil, prsStore{Store: st, prs: []domain.PullRequest{{URL: "https://github.com/o/r/pull/7", HeadSHA: tc.head}}})
			svc.SetFailureNotice(st, reports, &fakeWorkerNudger{})
			run, _, err := st.GetReviewRun(ctx, "approved")
			if err != nil {
				t.Fatal(err)
			}
			if err := svc.fileApprovalReport(ctx, svc.failureNotice(), run); err != nil {
				t.Fatal(err)
			}
			got, err := st.ListReportsBySession(ctx, "w-1")
			if err != nil || len(got) != 1 {
				t.Fatalf("reports=%+v err=%v", got, err)
			}
			const note = "Reviewed abc123, PR is now at newsha; re-review needed."
			if has := strings.Contains(got[0].Message, note); has != tc.wantNote {
				t.Fatalf("note present = %v, want %v: %q", has, tc.wantNote, got[0].Message)
			}
		})
	}
}
