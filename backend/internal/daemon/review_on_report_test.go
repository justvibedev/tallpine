package daemon

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/autoreview"
	"tallpine.eu/tallpine/backend/internal/domain"
	reviewcore "tallpine.eu/tallpine/backend/internal/review"
)

type reportStore struct{ project domain.ProjectRecord }

func (s reportStore) GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error) {
	return domain.SessionRecord{ID: "s1", ProjectID: "p1", Kind: domain.KindWorker, Harness: domain.AgentHarness("codex"), AutoReviewEnabled: true, Activity: domain.Activity{State: domain.ActivityActive}}, true, nil
}
func (s reportStore) ListLiveSessions(context.Context) ([]domain.SessionRecord, error) {
	return nil, nil
}
func (s reportStore) GetProject(context.Context, string) (domain.ProjectRecord, bool, error) {
	return s.project, true, nil
}
func (s reportStore) ListPRsBySession(context.Context, domain.SessionID) ([]domain.PullRequest, error) {
	return []domain.PullRequest{{URL: "pr1", Number: 1, HeadSHA: "sha1"}}, nil
}
func (s reportStore) ListReviewRunsBySession(context.Context, domain.SessionID) ([]domain.ReviewRun, error) {
	return nil, nil
}

type countTrigger struct{ calls atomic.Int32 }

func (t *countTrigger) TriggerAuto(context.Context, domain.SessionID, domain.ReviewerHarness) (reviewcore.TriggerResult, error) {
	t.calls.Add(1)
	return reviewcore.TriggerResult{Created: true}, nil
}

func TestReviewOnReportStartsReviewByDefault(t *testing.T) {
	off := false
	for _, tc := range []struct {
		name   string
		cfg    domain.ProjectConfig
		report domain.ReportRecord
		want   int32
	}{
		{"needs-input with pr_created, default config", domain.ProjectConfig{}, domain.ReportRecord{SessionID: "s1", State: domain.ReportNeedsInput, Outputs: []domain.ReportOutput{{Kind: domain.ReportOutputPRCreated}}}, 1},
		{"done report", domain.ProjectConfig{}, domain.ReportRecord{SessionID: "s1", State: domain.ReportDone}, 1},
		{"explicit opt-out", domain.ProjectConfig{AutoReviewOnReport: &off}, domain.ReportRecord{SessionID: "s1", State: domain.ReportDone}, 0},
		{"plain checkpoint", domain.ProjectConfig{}, domain.ReportRecord{SessionID: "s1", State: domain.ReportCheckpoint}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trig := &countTrigger{}
			b := &boot{ctx: context.Background(), autoReview: autoreview.New(reportStore{domain.ProjectRecord{ID: "p1", Config: tc.cfg}}, trig, autoreview.Config{})}
			b.reviewOnReport(tc.report)
			deadline := time.Now().Add(300 * time.Millisecond)
			for trig.calls.Load() < tc.want && time.Now().Before(deadline) {
				time.Sleep(5 * time.Millisecond)
			}
			time.Sleep(30 * time.Millisecond)
			if got := trig.calls.Load(); got != tc.want {
				t.Fatalf("trigger calls = %d, want %d", got, tc.want)
			}
		})
	}
}
