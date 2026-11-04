package merges_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/service/merges"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

// The digest reads the real PR/paths/lane tables: paths arrive through the
// same WritePR the SCM observer uses.
func TestDigestShowsInFlightByLaneOverlapsAndRecentMerges(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.MustOpen(t)
	now := time.Now().UTC().Truncate(time.Second)
	if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: "p", Path: "/tmp/p", RegisteredAt: now}); err != nil {
		t.Fatal(err)
	}
	session := func(lane string) domain.SessionID {
		rec, err := st.CreateSession(ctx, domain.SessionRecord{ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode})
		if err != nil {
			t.Fatal(err)
		}
		if lane != "" {
			if err := st.SetSessionLane(ctx, rec.ID, domain.SessionLane{LaneItemID: lane}); err != nil {
				t.Fatal(err)
			}
		}
		return rec.ID
	}
	write := func(n int, sid domain.SessionID, merged bool, mergedAt time.Time, paths []string) {
		t.Helper()
		pr := domain.PullRequest{URL: "https://github.com/o/r/pull/" + strings.Repeat("1", n), SessionID: sid, Number: n,
			Merged: merged, MergedAtProvider: mergedAt, UpdatedAt: now, Title: "pr", HeadSHA: "sha", Paths: paths}
		if err := st.WritePR(ctx, pr, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	write(1, session("lane-a"), false, time.Time{}, []string{"backend/a.go", "README.md"})
	write(2, session("lane-b"), false, time.Time{}, []string{"backend/a.go", "frontend/x.ts"})
	write(3, session(""), false, time.Time{}, []string{"docs/z.md"})
	write(4, session("lane-b"), true, now.Add(-time.Hour), []string{"frontend/x.ts"})
	write(5, session("lane-b"), true, now.Add(-72*time.Hour), []string{"docs/z.md"}) // outside the window

	d, err := merges.New(merges.Deps{Store: st, Now: func() time.Time { return now },
		LaneName: func(_ context.Context, id string) string { return strings.ToUpper(id) }}).Digest(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.RecentlyMerged) != 1 || d.RecentlyMerged[0].Number != 4 {
		t.Fatalf("recently merged = %+v, want only #4 (#5 is outside the window)", d.RecentlyMerged)
	}
	if len(d.InFlight) != 3 || d.InFlight[0].Number != 3 || d.InFlight[1].Lane != "LANE-A" || d.InFlight[2].Lane != "LANE-B" {
		t.Fatalf("in flight = %+v, want un-laned #3 then LANE-A #1 then LANE-B #2", d.InFlight)
	}
	var cross, vsMerged bool
	for _, o := range d.Overlaps {
		switch {
		case o.With == 1 && o.Number == 2 && !o.WithMerged && o.CrossLane && strings.Contains(o.Advice, "merge #1 first"):
			cross = true
		case o.With == 4 && o.Number == 2 && o.WithMerged:
			vsMerged = true
		default:
			t.Errorf("unexpected overlap %+v", o)
		}
	}
	if !cross || !vsMerged || len(d.Overlaps) != 2 {
		t.Fatalf("overlaps = %+v, want #2 behind #1 (cross-lane) and #2 vs merged #4", d.Overlaps)
	}
}

// An observation without a files list must not wipe the paths already stored.
func TestObservationWithoutPathsKeepsStoredPaths(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.MustOpen(t)
	now := time.Now().UTC().Truncate(time.Second)
	if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: "p", Path: "/tmp/p", RegisteredAt: now}); err != nil {
		t.Fatal(err)
	}
	rec, err := st.CreateSession(ctx, domain.SessionRecord{ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode})
	if err != nil {
		t.Fatal(err)
	}
	pr := domain.PullRequest{URL: "https://github.com/o/r/pull/9", SessionID: rec.ID, Number: 9, UpdatedAt: now, Paths: []string{"a.go"}}
	if err := st.WritePR(ctx, pr, nil, nil); err != nil {
		t.Fatal(err)
	}
	pr.Paths = nil
	if err := st.WritePR(ctx, pr, nil, nil); err != nil {
		t.Fatal(err)
	}
	got, err := st.MergeDigestPRs(ctx, "p", now.Add(-time.Hour))
	if err != nil || len(got) != 1 || len(got[0].Paths) != 1 || got[0].Paths[0] != "a.go" {
		t.Fatalf("digest prs = %+v err=%v, want stored path a.go kept", got, err)
	}
}

// A terminated session's PR is no longer polled, so a stored "open" state that
// has not changed within the window is stale and must not show as in flight (B209).
func TestDigestHidesStaleOpenPRsOfTerminatedSessions(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.MustOpen(t)
	now := time.Now().UTC().Truncate(time.Second)
	if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: "p", Path: "/tmp/p", RegisteredAt: now}); err != nil {
		t.Fatal(err)
	}
	add := func(n int, terminated bool, updated time.Time) {
		t.Helper()
		rec, err := st.CreateSession(ctx, domain.SessionRecord{ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, IsTerminated: terminated})
		if err != nil {
			t.Fatal(err)
		}
		pr := domain.PullRequest{URL: "https://github.com/o/r/pull/" + strings.Repeat("1", n), SessionID: rec.ID, Number: n, UpdatedAt: updated, Title: "pr", HeadSHA: "sha"}
		if err := st.WritePR(ctx, pr, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	add(1, false, now.Add(-120*time.Hour)) // live session: still polled, stays
	add(2, true, now.Add(-120*time.Hour))  // terminated + stale: hidden
	add(3, true, now.Add(-time.Hour))      // terminated but recently changed: stays
	d, err := merges.New(merges.Deps{Store: st, Now: func() time.Time { return now }}).Digest(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	got := map[int]bool{}
	for _, e := range d.InFlight {
		got[e.Number] = true
	}
	if len(got) != 2 || !got[1] || !got[3] {
		t.Fatalf("in flight = %+v, want #1 and #3 only", d.InFlight)
	}
}
