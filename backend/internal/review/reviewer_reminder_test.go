package review

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

type remindLauncher struct {
	*fakeLauncher
	sent []string
}

func (l *remindLauncher) Remind(_ context.Context, _ string, message string) error {
	l.sent = append(l.sent, message)
	return nil
}

// B651: reviewer 525e97ba ended its turn parked on a background grep and sat
// idle; the sweep failed the pass after the idle grace without ever waking it.
func TestIdleReviewerIsRemindedBeforeThePassFails(t *testing.T) {
	now := time.Unix(0, 0).UTC()
	clock := now
	store := &fakeStore{
		review: &domain.Review{
			ID: "rev-1", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode, ReviewerHandleID: "review-mer-1",
			ReviewerActivityState: domain.ActivityIdle, InterfaceMode: domain.ReviewerInterfaceTUI,
			UpdatedAt: now.Add(-reviewerIdleGrace),
		},
		runs: []domain.ReviewRun{{
			ID: "run-1", ReviewID: "rev-1", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode,
			PRURL: "https://github.com/o/r/pull/1", TargetSHA: "sha1", Status: domain.ReviewRunRunning,
			CreatedAt: now.Add(-reviewerIdleGrace - time.Minute),
		}},
	}
	launcher := &remindLauncher{fakeLauncher: &fakeLauncher{alive: true}}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, launcher)
	eng.clock = func() time.Time { return clock }
	status := func() domain.ReviewRunStatus {
		got, err := eng.List(context.Background(), "mer-1")
		if err != nil || len(got.Runs) != 1 {
			t.Fatalf("List: %v, %+v", err, got.Runs)
		}
		return got.Runs[0].Status
	}

	if status() != domain.ReviewRunRunning || len(launcher.sent) != 1 {
		t.Fatalf("first sweep: want running + one reminder, got sent=%d", len(launcher.sent))
	}
	// The reminder woke it and it went idle again, but not for long enough yet.
	clock = clock.Add(time.Minute)
	store.review.UpdatedAt = clock
	if status() != domain.ReviewRunRunning || len(launcher.sent) != 1 {
		t.Fatalf("within the reminder grace: want running and no second reminder, sent=%d", len(launcher.sent))
	}
	// Still idle after the reminder grace: now it fails.
	clock = clock.Add(reviewerReminderGrace)
	if status() != domain.ReviewRunFailed || len(launcher.sent) != 1 {
		t.Fatalf("after the reminder grace: want failed, sent=%d", len(launcher.sent))
	}
}

type failingRemindLauncher struct {
	*remindLauncher
}

func (l failingRemindLauncher) Remind(context.Context, string, string) error {
	return context.DeadlineExceeded
}

func batchedIdleFixture(now time.Time) *fakeStore {
	run := func(id string) domain.ReviewRun {
		return domain.ReviewRun{
			ID: id, ReviewID: "rev-1", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode,
			PRURL: "https://github.com/o/r/pull/1", TargetSHA: "sha1", Status: domain.ReviewRunRunning,
			CreatedAt: now.Add(-reviewerIdleGrace - time.Minute),
		}
	}
	return &fakeStore{
		review: &domain.Review{
			ID: "rev-1", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode, ReviewerHandleID: "review-mer-1",
			ReviewerActivityState: domain.ActivityIdle, InterfaceMode: domain.ReviewerInterfaceTUI,
			UpdatedAt: now.Add(-reviewerIdleGrace),
		},
		runs: []domain.ReviewRun{run("run-1"), run("run-2")},
	}
}

// A batch holds several running runs on one pane: it is woken once, and every
// run fails together after the reminder grace.
func TestBatchedIdleReviewerIsRemindedOnce(t *testing.T) {
	now := time.Unix(0, 0).UTC()
	clock := now
	store := batchedIdleFixture(now)
	launcher := &remindLauncher{fakeLauncher: &fakeLauncher{alive: true}}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, launcher)
	eng.clock = func() time.Time { return clock }
	list := func() []domain.ReviewRun {
		got, err := eng.List(context.Background(), "mer-1")
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		return got.Runs
	}
	list()
	if len(launcher.sent) != 1 {
		t.Fatalf("reminders = %d, want 1 for the whole batch", len(launcher.sent))
	}
	clock = clock.Add(reviewerReminderGrace)
	store.review.UpdatedAt = now
	for _, r := range list() {
		if r.Status != domain.ReviewRunFailed {
			t.Fatalf("run %s = %s, want failed", r.ID, r.Status)
		}
	}
	if len(launcher.sent) != 1 {
		t.Fatalf("reminders = %d, want still 1", len(launcher.sent))
	}
}

func TestFailedReminderFailsThePassAtOnce(t *testing.T) {
	now := time.Unix(0, 0).UTC()
	store := batchedIdleFixture(now)
	launcher := failingRemindLauncher{&remindLauncher{fakeLauncher: &fakeLauncher{alive: true}}}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, launcher)
	got, err := eng.List(context.Background(), "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range got.Runs {
		if r.Status != domain.ReviewRunFailed {
			t.Fatalf("run %s = %s, want failed", r.ID, r.Status)
		}
	}
}
