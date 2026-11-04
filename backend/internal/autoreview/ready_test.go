package autoreview

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestEvaluateReadyTriggersWithoutIdleUnlessProjectOptsOut(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	off := false
	for _, tc := range []struct {
		name    string
		onRep   *bool
		active  bool
		enabled bool
		ready   bool
		want    bool
	}{
		{"default on, worker still active", nil, true, true, true, true},
		{"project opted out", &off, true, true, true, false},
		{"session auto-review off", nil, true, false, true, false},
		{"idle sweep path still needs idle", nil, true, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := domain.ActivityIdle
			if tc.active {
				state = domain.ActivityActive
			}
			store := &fakeStore{
				session: domain.SessionRecord{ID: "s1", ProjectID: "p1", Kind: domain.KindWorker, Harness: domain.AgentHarness("codex"), AutoReviewEnabled: tc.enabled, Activity: domain.Activity{State: state, LastActivityAt: now}},
				project: domain.ProjectRecord{ID: "p1", Config: domain.ProjectConfig{AutoReviewOnReport: tc.onRep}},
				prs:     []domain.PullRequest{{URL: "pr1", Number: 1, HeadSHA: "sha1"}},
			}
			trigger := &fakeTrigger{}
			c := New(store, trigger, Config{Clock: func() time.Time { return now }})
			var res Result
			var err error
			if tc.ready {
				res, err = c.EvaluateReady(context.Background(), "s1")
			} else {
				res, err = c.EvaluateSession(context.Background(), "s1")
			}
			if err != nil {
				t.Fatal(err)
			}
			if res.Triggered != tc.want || (trigger.calls == 1) != tc.want {
				t.Fatalf("triggered=%v calls=%d want %v (reason=%s)", res.Triggered, trigger.calls, tc.want, res.Reason)
			}
		})
	}
}
