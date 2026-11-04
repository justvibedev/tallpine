package autoreview

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestAutoReviewStopsAfterMaxRoundsAndEscalates(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	changes := func(sha string) domain.ReviewRun {
		return domain.ReviewRun{PRURL: "pr1", TargetSHA: sha, Status: domain.ReviewRunComplete, Verdict: domain.VerdictChangesRequested, TriggerSource: domain.ReviewTriggerAuto, CreatedAt: now}
	}
	for _, tc := range []struct {
		name      string
		max       int
		want      bool
		escalated bool
	}{
		{"limit off never stops", 0, true, false},
		{"below the limit reviews again", 3, true, false},
		{"at the limit stops and escalates", 2, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{
				session: domain.SessionRecord{ID: "s1", ProjectID: "p1", Kind: domain.KindWorker, Harness: domain.AgentHarness("codex"), AutoReviewEnabled: true, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now.Add(-time.Minute)}},
				project: domain.ProjectRecord{ID: "p1", Config: domain.ProjectConfig{AutoReviewMaxRounds: tc.max}},
				prs:     []domain.PullRequest{{URL: "pr1", Number: 1, HeadSHA: "sha3"}},
				runs:    []domain.ReviewRun{changes("sha1"), changes("sha2")},
			}
			trigger := &fakeTrigger{}
			var escalatedRounds int
			c := New(store, trigger, Config{Clock: func() time.Time { return now }, Escalate: func(_ context.Context, _ domain.SessionRecord, _, _ string, rounds int) { escalatedRounds = rounds }})
			res, err := c.EvaluateSession(context.Background(), "s1")
			if err != nil {
				t.Fatal(err)
			}
			if res.Triggered != tc.want || (escalatedRounds == 2) != tc.escalated {
				t.Fatalf("triggered=%v escalatedRounds=%d reason=%s, want triggered=%v escalated=%v", res.Triggered, escalatedRounds, res.Reason, tc.want, tc.escalated)
			}
			if !tc.want && res.Reason != "review_rounds_exhausted" {
				t.Fatalf("reason=%s", res.Reason)
			}
		})
	}
}

// An approval ends the loop: change requests before it no longer count.
func TestAutoReviewRoundsResetAfterApproval(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	run := func(sha string, v domain.ReviewVerdict, ago time.Duration) domain.ReviewRun {
		return domain.ReviewRun{PRURL: "pr1", TargetSHA: sha, Status: domain.ReviewRunComplete, Verdict: v, TriggerSource: domain.ReviewTriggerAuto, CreatedAt: now.Add(-ago)}
	}
	store := &fakeStore{
		session: domain.SessionRecord{ID: "s1", ProjectID: "p1", Kind: domain.KindWorker, Harness: domain.AgentHarness("codex"), AutoReviewEnabled: true, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now.Add(-time.Minute)}},
		project: domain.ProjectRecord{ID: "p1", Config: domain.ProjectConfig{AutoReviewMaxRounds: 2}},
		prs:     []domain.PullRequest{{URL: "pr1", Number: 1, HeadSHA: "sha4"}},
		runs: []domain.ReviewRun{
			run("sha1", domain.VerdictChangesRequested, 4*time.Hour), run("sha2", domain.VerdictChangesRequested, 3*time.Hour),
			run("sha3", domain.VerdictApproved, 2*time.Hour),
		},
	}
	trigger := &fakeTrigger{}
	res, err := New(store, trigger, Config{Clock: func() time.Time { return now }}).EvaluateSession(context.Background(), "s1")
	if err != nil || !res.Triggered {
		t.Fatalf("triggered=%v err=%v reason=%s, want a review after the approval reset the count", res.Triggered, err, res.Reason)
	}
}
