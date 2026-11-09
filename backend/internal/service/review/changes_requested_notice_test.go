package review

import (
	"context"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

const changesRequestedPR = "https://github.com/o/r/pull/7"

// refusingNudger models the guard refusing a write while the worker awaits its user.
type refusingNudger struct {
	refuse   int
	messages []string
}

func (n *refusingNudger) Nudge(_ context.Context, _ domain.SessionID, message string) (bool, error) {
	if n.refuse > 0 {
		n.refuse--
		return false, nil
	}
	n.messages = append(n.messages, message)
	return true, nil
}

func seedChangesRequested(t *testing.T, st *sqlite.Store, autoInject bool, at time.Time) {
	seedChangesRequestedFull(t, st, autoInject, at, "sha-a", "gh-1")
}

func seedChangesRequestedAt(t *testing.T, st *sqlite.Store, autoInject bool, at time.Time, prHead string) {
	t.Helper()
	seedChangesRequestedFull(t, st, autoInject, at, prHead, "gh-1")
}

func seedChangesRequestedFull(t *testing.T, st *sqlite.Store, autoInject bool, at time.Time, prHead, ghReview string) {
	t.Helper()
	ctx := context.Background()
	seedNoticeWorker(t, st, "w-1")
	pr := domain.PullRequest{URL: changesRequestedPR, SessionID: "w-1", Number: 7, Title: "t", HeadSHA: prHead, UpdatedAt: at}
	comments := []domain.PullRequestComment{
		{ID: "c1", ThreadID: "t1", ReviewID: "gh-1", File: "a.go", Line: 12, Body: "fix", CreatedAt: at, AutoInjectReview: true},
		{ID: "c0", ThreadID: "t0", ReviewID: "gh-old", File: "old.go", Line: 1, Body: "older review", CreatedAt: at, AutoInjectReview: true},
		{ID: "c2", ThreadID: "t2", File: "b.go", Line: 3, Body: "done", Resolved: true, CreatedAt: at, AutoInjectReview: true},
	}
	if err := st.WriteSCMObservation(ctx, pr, nil, nil, nil, comments, ports.ReviewWriteReplace); err != nil {
		t.Fatalf("seed pr: %v", err)
	}
	seedEndedRun(t, st, "run-cr", "w-1", "sha-a", domain.ReviewRunRunning, "", at)
	if ok, err := st.UpdateReviewRunResult(ctx, "run-cr", domain.ReviewRunComplete, domain.VerdictChangesRequested, "Missing nil check in a.go", ghReview, autoInject); err != nil || !ok {
		t.Fatalf("complete run: ok=%v err=%v", ok, err)
	}
}

func TestNotifyEndedRunsWakesWorkerOnceForChangesRequested(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.MustOpen(t)
	t0 := time.Unix(1000, 0).UTC()
	seedChangesRequested(t, st, true, t0)
	nudger := &fakeWorkerNudger{}
	svc := New(nil, st, WithClock(func() time.Time { return t0.Add(time.Minute) }))
	svc.SetFailureNotice(st, &fakeFailureReports{}, nudger)

	for i := 0; i < 2; i++ { // the second sweep stands in for a restart re-listing the run
		if err := svc.NotifyEndedRuns(ctx); err != nil {
			t.Fatalf("NotifyEndedRuns %d: %v", i, err)
		}
	}
	if len(nudger.messages) != 1 {
		t.Fatalf("nudges = %q, want exactly one wake-up for the review run", nudger.messages)
	}
	for _, want := range []string{"requested changes", changesRequestedPR, "Missing nil check", "a.go:12"} {
		if !strings.Contains(nudger.messages[0], want) {
			t.Fatalf("message %q lacks %q", nudger.messages[0], want)
		}
	}
	if strings.Contains(nudger.messages[0], "old.go") {
		t.Fatalf("message %q lists a thread of an older review", nudger.messages[0])
	}
	if strings.Contains(nudger.messages[0], "b.go") {
		t.Fatalf("message %q lists a resolved thread", nudger.messages[0])
	}
}

func TestNotifyEndedRunsRetriesRefusedChangesRequestedNudge(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.MustOpen(t)
	t0 := time.Unix(1000, 0).UTC()
	seedChangesRequested(t, st, true, t0)
	nudger := &refusingNudger{refuse: 1}
	svc := New(nil, st, WithClock(func() time.Time { return t0.Add(time.Minute) }))
	svc.SetFailureNotice(st, &fakeFailureReports{}, nudger)

	for i := 0; i < 3; i++ {
		if err := svc.NotifyEndedRuns(ctx); err != nil {
			t.Fatalf("NotifyEndedRuns %d: %v", i, err)
		}
	}
	if len(nudger.messages) != 1 {
		t.Fatalf("nudges = %q, want one delivery after the refused first attempt", nudger.messages)
	}
}

func TestNotifyEndedRunsSkipsOptedOutAndStaleChangesRequested(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		autoInject bool
		age        time.Duration
	}{
		"opted out": {false, time.Minute},
		"stale":     {true, endedRunNoticeRetryWindow + time.Hour},
	} {
		t.Run(name, func(t *testing.T) {
			st := sqlitetest.MustOpen(t)
			t0 := time.Unix(1000, 0).UTC()
			seedChangesRequested(t, st, tc.autoInject, t0)
			nudger := &fakeWorkerNudger{}
			svc := New(nil, st, WithClock(func() time.Time { return t0.Add(tc.age) }))
			svc.SetFailureNotice(st, &fakeFailureReports{}, nudger)
			if err := svc.NotifyEndedRuns(ctx); err != nil {
				t.Fatalf("NotifyEndedRuns: %v", err)
			}
			if len(nudger.messages) != 0 {
				t.Fatalf("nudges = %q, want none", nudger.messages)
			}
			if left, err := st.ListUnnotifiedEndedReviewRuns(ctx, 10); err != nil || len(left) != 0 {
				t.Fatalf("unnotified = %+v err=%v, want the run claimed", left, err)
			}
		})
	}
}

func TestNotifyEndedRunsSkipsChangesRequestedForASupersededHead(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.MustOpen(t)
	t0 := time.Unix(1000, 0).UTC()
	seedChangesRequestedAt(t, st, true, t0, "sha-newer") // the run reviewed sha-a
	nudger := &fakeWorkerNudger{}
	svc := New(nil, st, WithClock(func() time.Time { return t0.Add(time.Minute) }))
	svc.SetFailureNotice(st, &fakeFailureReports{}, nudger)
	if err := svc.NotifyEndedRuns(ctx); err != nil {
		t.Fatalf("NotifyEndedRuns: %v", err)
	}
	if len(nudger.messages) != 0 {
		t.Fatalf("nudges = %q, want none for feedback on an old head", nudger.messages)
	}
	if left, err := st.ListUnnotifiedEndedReviewRuns(ctx, 10); err != nil || len(left) != 0 {
		t.Fatalf("unnotified = %+v err=%v, want the run claimed", left, err)
	}
}

// The sweep can run before the observer has fetched the review's inline
// comments: the message must not present older threads as this review's, and
// must say the comments are still syncing.
func TestChangesRequestedWakeBeforeObserverSyncListsNoStaleThreads(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.MustOpen(t)
	t0 := time.Unix(1000, 0).UTC()
	seedChangesRequestedFull(t, st, true, t0, "sha-a", "gh-new") // the observer has fetched only older reviews
	nudger := &fakeWorkerNudger{}
	svc := New(nil, st, WithClock(func() time.Time { return t0.Add(time.Minute) }))
	svc.SetFailureNotice(st, &fakeFailureReports{}, nudger)
	if err := svc.NotifyEndedRuns(ctx); err != nil {
		t.Fatalf("NotifyEndedRuns: %v", err)
	}
	if len(nudger.messages) != 1 {
		t.Fatalf("nudges = %q, want one", nudger.messages)
	}
	msg := nudger.messages[0]
	if strings.Contains(msg, "a.go:12") || strings.Contains(msg, "old.go") {
		t.Fatalf("message %q lists threads that are not from this review", msg)
	}
	if !strings.Contains(msg, "Missing nil check") || !strings.Contains(msg, "reach you as the PR syncs") {
		t.Fatalf("message %q lacks the summary or the syncing note", msg)
	}
}

// B590: when the review never reached the PR no inline comment will sync, so the
// worker needs the findings themselves, not a promise and a trimmed summary.
func TestChangesRequestedMessageCarriesFindingsWhenNotPosted(t *testing.T) {
	findings := strings.Repeat("x", 900) + " the last finding"
	run := domain.ReviewRun{Harness: domain.ReviewerClaudeCode, PRURL: "https://github.com/o/r/pull/1", Status: domain.ReviewRunComplete, Verdict: domain.VerdictChangesRequested, Body: findings}
	msg := changesRequestedMessage(run, domain.PullRequest{Number: 1}, nil)
	if !strings.Contains(msg, "could not post this review") || !strings.Contains(msg, "the last finding") {
		t.Fatalf("message must say the post failed and carry every finding: %q", msg)
	}
	if strings.Contains(msg, "as the PR syncs") {
		t.Fatalf("message promises inline comments that will never arrive: %q", msg)
	}
	run.GithubReviewID = "12345"
	if msg := changesRequestedMessage(run, domain.PullRequest{Number: 1}, nil); strings.Contains(msg, "could not post") {
		t.Fatalf("a posted review must not read as a failure: %q", msg)
	}
}
