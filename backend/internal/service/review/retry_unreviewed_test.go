package review

import (
	"context"
	"fmt"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	reviewcore "tallpine.eu/tallpine/backend/internal/review"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

func TestRestartAndTrustDialogPassesDoNotUseUpTheCommitsRetry(t *testing.T) {
	for _, tc := range []struct {
		name  string
		first []string
		want  int
	}{
		{"restart", []string{reviewcore.ReviewerInterruptedByRestart}, 1},
		{"trust dialog", []string{reviewcore.ReviewerNotStartedPrefix + "Claude is waiting on its workspace trust dialog (x)"}, 1},
		{"bounded", []string{reviewcore.ReviewerInterruptedByRestart, reviewcore.ReviewerInterruptedByRestart, reviewcore.ReviewerInterruptedByRestart}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st := sqlitetest.MustOpen(t)
			seedNoticeWorker(t, st, "w-1")
			seedRetryPR(t, st, "sha-a")
			t0 := time.Unix(1000, 0).UTC()
			for i, body := range tc.first {
				seedEndedRun(t, st, fmt.Sprintf("run-%d", i), "w-1", "sha-a", domain.ReviewRunFailed, body, t0.Add(time.Duration(i)*time.Minute))
			}
			seedEndedRun(t, st, "run-last", "w-1", "sha-a", domain.ReviewRunFailed, "reviewer turn ended without submitting a result", t0.Add(time.Hour))
			svc := New(nil, st, WithClock(func() time.Time { return t0.Add(2 * time.Hour) }))
			svc.SetFailureNotice(st, &fakeFailureReports{}, &fakeWorkerNudger{})
			triggers := 0
			svc.engineTrigger = func(context.Context, domain.SessionID, reviewcore.TriggerOptions) (reviewcore.TriggerResult, error) {
				triggers++
				return reviewcore.TriggerResult{Created: true, CreatedRuns: []domain.ReviewRun{{ID: "x"}}}, nil
			}
			if err := svc.NotifyEndedRuns(ctx); err != nil {
				t.Fatal(err)
			}
			if triggers != tc.want {
				t.Fatalf("triggers = %d, want %d", triggers, tc.want)
			}
		})
	}
}

func TestUsageLimitedPassDoesNotUseUpTheCommitsRetry(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.MustOpen(t)
	seedNoticeWorker(t, st, "w-1")
	seedRetryPR(t, st, "sha-a")
	t0 := time.Unix(1000, 0).UTC()
	limit := reviewcore.ReviewerUsageLimitPrefix + "You've hit your 5-hour limit · resets " + t0.Add(time.Minute).Format(time.RFC3339)
	seedEndedRun(t, st, "run-1", "w-1", "sha-a", domain.ReviewRunFailed, limit, t0)
	seedEndedRun(t, st, "run-2", "w-1", "sha-a", domain.ReviewRunFailed, "reviewer turn ended without submitting a result", t0.Add(2*time.Minute))

	svc := New(nil, st, WithClock(func() time.Time { return t0.Add(time.Hour) }))
	svc.SetFailureNotice(st, &fakeFailureReports{}, &fakeWorkerNudger{})
	triggers := 0
	svc.engineTrigger = func(context.Context, domain.SessionID, reviewcore.TriggerOptions) (reviewcore.TriggerResult, error) {
		triggers++
		return reviewcore.TriggerResult{Created: true, CreatedRuns: []domain.ReviewRun{{ID: "run-3"}}}, nil
	}
	if err := svc.NotifyEndedRuns(ctx); err != nil {
		t.Fatal(err)
	}
	if triggers != 1 {
		t.Fatalf("triggers = %d, want one retry after the limit-only pass and one idle failure", triggers)
	}
}
