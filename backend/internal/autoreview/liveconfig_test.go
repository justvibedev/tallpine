package autoreview

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func liveStore(now time.Time) *fakeStore {
	return &fakeStore{
		session: domain.SessionRecord{ID: "s1", ProjectID: "p1", Kind: domain.KindWorker, Harness: domain.AgentHarness("codex"), Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now.Add(-time.Minute)}},
		project: domain.ProjectRecord{ID: "p1"},
		prs:     []domain.PullRequest{{URL: "pr1", Number: 1, HeadSHA: "sha1"}},
	}
}

// B389: a worker spawned while Auto review was off is covered once the project
// turns it on.
func TestProjectToggleCoversAlreadyRunningWorker(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	store := liveStore(now)
	on := true
	trigger := &fakeTrigger{}
	c := New(store, trigger, Config{Clock: func() time.Time { return now }})
	if r, _ := c.EvaluateSession(context.Background(), "s1"); r.Triggered || r.Reason != "disabled" {
		t.Fatalf("setting off: %+v", r)
	}
	store.project.Config.AutoReview = &on
	if r, err := c.EvaluateSession(context.Background(), "s1"); err != nil || !r.Triggered {
		t.Fatalf("project on must cover the live worker: %+v %v", r, err)
	}
}

func TestStalledReviewIsEscalatedOncePerHead(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	store := liveStore(now)
	on := true
	store.project.Config.AutoReview = &on
	var got []time.Duration
	c := New(store, &fakeTrigger{}, Config{
		Clock:   func() time.Time { return now },
		Stalled: func(_ context.Context, _ domain.SessionRecord, _, _ string, w time.Duration) { got = append(got, w) },
	})
	ctx := context.Background()
	_ = c.Sweep(ctx)
	now = now.Add(9 * time.Minute)
	_ = c.Sweep(ctx)
	if len(got) != 0 {
		t.Fatalf("escalated before threshold: %v", got)
	}
	now = now.Add(2 * time.Minute)
	_ = c.Sweep(ctx)
	_ = c.Sweep(ctx)
	if len(got) != 1 || got[0] < DefaultStallThreshold {
		t.Fatalf("want one escalation past threshold, got %v", got)
	}
}

type overrideStore struct {
	*fakeStore
	enabled bool
}

func (o overrideStore) SessionAutoReviewOverride(context.Context, domain.SessionID) (bool, bool, error) {
	return o.enabled, true, nil
}

// A deliberate per-session disable wins over the project setting.
func TestSessionOverrideBeatsProjectSetting(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	base := liveStore(now)
	on := true
	base.project.Config.AutoReview = &on
	trigger := &fakeTrigger{}
	r, _ := New(overrideStore{base, false}, trigger, Config{Clock: func() time.Time { return now }}).EvaluateSession(context.Background(), "s1")
	if r.Triggered || r.Reason != "disabled" {
		t.Fatalf("explicit off must win: %+v", r)
	}
	base.project.Config.AutoReview = nil
	r, _ = New(overrideStore{base, true}, trigger, Config{Clock: func() time.Time { return now }}).EvaluateSession(context.Background(), "s1")
	if !r.Triggered {
		t.Fatalf("explicit on must win: %+v", r)
	}
}

// Turning the project off stops a worker that was spawned with the flag on.
func TestProjectOffStopsWorkerSpawnedWithFlagOn(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	store := liveStore(now)
	store.session.AutoReviewEnabled = true
	off := false
	store.project.Config.AutoReview = &off
	trigger := &fakeTrigger{}
	r, _ := New(store, trigger, Config{Clock: func() time.Time { return now }}).EvaluateSession(context.Background(), "s1")
	if r.Triggered || r.Reason != "disabled" || trigger.calls != 0 {
		t.Fatalf("project off must stop a flagged worker: %+v", r)
	}
}

// A stall is only a review that should have started: not when Auto review is
// off (project or this session) and not while the worker is busy.
func TestNoStallReportWhenReviewIsNotDue(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	on, off := true, false
	for _, tc := range []struct {
		name  string
		build func() Store
	}{
		{"project off", func() Store { s := liveStore(now); s.project.Config.AutoReview = &off; return s }},
		{"explicit session off", func() Store {
			s := liveStore(now)
			s.project.Config.AutoReview = &on
			return overrideStore{s, false}
		}},
		{"worker busy", func() Store {
			s := liveStore(now)
			s.project.Config.AutoReview = &on
			s.session.Activity.State = domain.ActivityActive
			return s
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clock := now
			n := 0
			c := New(tc.build(), &fakeTrigger{}, Config{
				Clock:   func() time.Time { return clock },
				Stalled: func(context.Context, domain.SessionRecord, string, string, time.Duration) { n++ },
			})
			_ = c.Sweep(context.Background())
			clock = now.Add(30 * time.Minute)
			_ = c.Sweep(context.Background())
			if n != 0 {
				t.Fatalf("stall reported %d times though no review is due", n)
			}
		})
	}
}

// A head evaluate blocks on purpose (cancelled run, rounds used up) is not a
// stall.
func TestNoStallReportForHeadsAutoReviewBlocksOnPurpose(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		mutate func(*fakeStore)
	}{
		{"cancelled on current head", func(s *fakeStore) {
			s.runs = []domain.ReviewRun{{PRURL: "pr1", TargetSHA: "sha1", Status: domain.ReviewRunCancelled, CreatedAt: now}}
		}},
		{"rounds exhausted", func(s *fakeStore) {
			s.project.Config.AutoReviewMaxRounds = 1
			s.runs = []domain.ReviewRun{{PRURL: "pr1", TargetSHA: "old", Status: domain.ReviewRunComplete, Verdict: domain.VerdictChangesRequested, CreatedAt: now}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := liveStore(now)
			on := true
			store.project.Config.AutoReview = &on
			tc.mutate(store)
			clock, n := now, 0
			c := New(store, &fakeTrigger{}, Config{
				Clock:   func() time.Time { return clock },
				Stalled: func(context.Context, domain.SessionRecord, string, string, time.Duration) { n++ },
			})
			_ = c.Sweep(context.Background())
			clock = now.Add(30 * time.Minute)
			_ = c.Sweep(context.Background())
			if n != 0 {
				t.Fatalf("stall reported %d times for a head auto review blocks on purpose", n)
			}
		})
	}
}

type syncStore struct {
	*fakeStore
	synced []bool
}

func (s *syncStore) SyncSessionAutoReviewFlag(_ context.Context, _ domain.SessionID, enabled bool, _ time.Time) (bool, error) {
	s.synced = append(s.synced, enabled)
	return true, nil
}

// The stored flag (API, kanban, UI) follows the setting on running workers.
func TestSweepSyncsStoredFlagWithEffectiveSetting(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	base := liveStore(now)
	on := true
	base.project.Config.AutoReview = &on
	st := &syncStore{fakeStore: base}
	_ = New(st, &fakeTrigger{}, Config{Clock: func() time.Time { return now }}).Sweep(context.Background())
	if len(st.synced) != 1 || !st.synced[0] {
		t.Fatalf("synced = %v, want [true]", st.synced)
	}
}

// A review that ran on the stronger risk reviewer (another harness than the
// configured one) means the head is not stalled.
func TestNoStallReportWhenAnotherAgentReviewedTheHead(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	for _, run := range []domain.ReviewRun{
		{PRURL: "pr1", TargetSHA: "sha1", Harness: domain.ReviewerClaudeCode, Status: domain.ReviewRunRunning, CreatedAt: now},
		{PRURL: "pr1", TargetSHA: "sha1", Harness: domain.ReviewerClaudeCode, Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved, CreatedAt: now},
	} {
		store := liveStore(now)
		on := true
		store.project.Config.AutoReview = &on
		store.session.ReviewerHarness = domain.ReviewerCodex
		store.runs = []domain.ReviewRun{run}
		clock, n := now, 0
		c := New(store, &fakeTrigger{}, Config{
			Clock:   func() time.Time { return clock },
			Stalled: func(context.Context, domain.SessionRecord, string, string, time.Duration) { n++ },
		})
		_ = c.Sweep(context.Background())
		clock = now.Add(30 * time.Minute)
		_ = c.Sweep(context.Background())
		if n != 0 {
			t.Fatalf("stall reported for a head another agent reviewed (%s)", run.Status)
		}
	}
}

// Auto review giving up after three failed runs on a head is a failure to
// surface, exactly once.
func TestStallReportedWhenAutoRetriesAreExhausted(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	store := liveStore(now)
	on := true
	store.project.Config.AutoReview = &on
	for i := 0; i < 3; i++ {
		store.runs = append(store.runs, domain.ReviewRun{PRURL: "pr1", TargetSHA: "sha1", Status: domain.ReviewRunFailed, TriggerSource: domain.ReviewTriggerAuto, CreatedAt: now.Add(time.Duration(i) * time.Second)})
	}
	clock, n := now, 0
	c := New(store, &fakeTrigger{}, Config{
		Clock:   func() time.Time { return clock },
		Stalled: func(context.Context, domain.SessionRecord, string, string, time.Duration) { n++ },
	})
	_ = c.Sweep(context.Background())
	clock = now.Add(30 * time.Minute)
	_ = c.Sweep(context.Background())
	_ = c.Sweep(context.Background())
	if n != 1 {
		t.Fatalf("stall reports = %d, want exactly 1", n)
	}
}
