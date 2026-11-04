package store_test

import (
	"context"
	"testing"
	"time"

	sqlitestore "tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

func TestCleanupPolicyRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	doc, _, err := s.GetCleanupPolicy(ctx)
	if err != nil || doc != "{}" {
		t.Fatalf("seeded policy = %q, %v; want {}", doc, err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := s.SetCleanupPolicy(ctx, `{"automatic":false}`, now); err != nil {
		t.Fatal(err)
	}
	doc, updated, err := s.GetCleanupPolicy(ctx)
	if err != nil || doc != `{"automatic":false}` || !updated.Equal(now) {
		t.Fatalf("policy = %q at %v, %v", doc, updated, err)
	}
	if err := s.SetCleanupPolicy(ctx, `not json`, now); err == nil {
		t.Fatal("an invalid policy document was stored")
	}
}

func TestCleanupRunsHistoryAndTrim(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Second)
	insert := func(i int, pass string, dry bool) int64 {
		t.Helper()
		id, err := s.InsertCleanupRun(ctx, sqlitestore.CleanupRun{
			StartedAt: base.Add(time.Duration(i) * time.Minute), FinishedAt: base.Add(time.Duration(i) * time.Minute),
			Trigger: "schedule", Pass: pass, DryRun: dry, FreedBytes: int64(i), Report: `{}`,
		})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	insert(1, "full", false)
	insert(2, "light", false)
	dry := insert(3, "full", true)
	last := insert(4, "light", false)

	if run, ok, err := s.LatestCleanupRun(ctx, false); err != nil || !ok || run.ID != last {
		t.Fatalf("latest real run = %+v %t %v", run, ok, err)
	}
	if run, ok, err := s.LatestCleanupRun(ctx, true); err != nil || !ok || run.ID != dry || !run.DryRun {
		t.Fatalf("latest dry run = %+v %t %v", run, ok, err)
	}
	if err := s.TrimCleanupRuns(ctx, false, 2); err != nil {
		t.Fatal(err)
	}
	runs, err := s.ListCleanupRuns(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 3 || runs[0].ID != last || runs[1].ID != dry {
		t.Fatalf("after trim = %+v, want the two newest real runs and the dry run", runs)
	}
	if err := s.TrimCleanupRuns(ctx, false, 5); err != nil {
		t.Fatal(err)
	}
	if runs, _ := s.ListCleanupRuns(ctx, 10); len(runs) != 3 {
		t.Fatalf("trim below the cap removed runs: %d left", len(runs))
	}
}
