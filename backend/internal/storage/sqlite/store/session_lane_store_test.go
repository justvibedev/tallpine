package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/cdc"
	"tallpine.eu/tallpine/backend/internal/domain"
)

// CreateReport stamps the worker's lane from session_lane, and a claim only
// takes the requested lane's reports.
func TestReportLaneIsStampedAtCreateAndScopesTheClaim(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "tp")
	mk := func(lane string) domain.SessionID {
		rec, err := s.CreateSession(ctx, sampleRecord("tp"))
		if err != nil {
			t.Fatal(err)
		}
		if lane != "" {
			if err := s.SetSessionLane(ctx, rec.ID, domain.SessionLane{LaneItemID: lane, ParentSessionID: "tp-0"}); err != nil {
				t.Fatal(err)
			}
		}
		return rec.ID
	}
	wa, wb, wn := mk("lane-a"), mk("lane-b"), mk("")
	now := time.Now().UTC().Truncate(time.Second)
	for id, worker := range map[string]domain.SessionID{"ra": wa, "rb": wb, "rn": wn} {
		if _, err := s.CreateReport(ctx, domain.ReportRecord{ID: id, SessionID: worker, ProjectID: "tp", Message: id, CreatedAt: now, DeliveryState: domain.ReportPending, AvailableAt: now, RepeatCount: 1}); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := s.SessionLane(ctx, wa); err != nil || got.LaneItemID != "lane-a" || got.ParentSessionID != "tp-0" {
		t.Fatalf("SessionLane = %+v err=%v", got, err)
	}
	lanes, err := s.ListPendingReportLanes(ctx, "tp", now)
	if err != nil || len(lanes) != 3 {
		t.Fatalf("pending lanes = %v err=%v, want lane-a, lane-b and un-laned", lanes, err)
	}
	for lane, want := range map[string]string{"lane-a": "ra", "lane-b": "rb", "": "rn"} {
		got, err := s.ClaimPendingReportBatch(ctx, "tp", lane, "tok-"+lane, now)
		if err != nil || len(got) != 1 || got[0].ID != want {
			t.Fatalf("claim lane %q = %+v err=%v, want only %s", lane, got, err, want)
		}
	}
}

func TestPendingReportLanesOnlyListsDueReports(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "tp")
	rec, err := s.CreateSession(ctx, sampleRecord("tp"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetSessionLane(ctx, rec.ID, domain.SessionLane{LaneItemID: "lane-a"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := s.CreateReport(ctx, domain.ReportRecord{ID: "r", SessionID: rec.ID, ProjectID: "tp", Message: "m", CreatedAt: now, DeliveryState: domain.ReportPending, AvailableAt: now.Add(time.Hour), RepeatCount: 1}); err != nil {
		t.Fatal(err)
	}
	if lanes, err := s.ListPendingReportLanes(ctx, "tp", now); err != nil || len(lanes) != 0 {
		t.Fatalf("lanes before due = %v err=%v, want none", lanes, err)
	}
	if lanes, err := s.ListPendingReportLanes(ctx, "tp", now.Add(2*time.Hour)); err != nil || len(lanes) != 1 || lanes[0] != "lane-a" {
		t.Fatalf("lanes after due = %v err=%v", lanes, err)
	}
}

// EndLane moves the lane's workers and pending reports to the default lane and
// deletes the terminated orchestrator row, FK-bound change_log rows included,
// and nothing of another lane or project.
func TestEndLaneRehomesAndDeletesTheOrchestrator(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "tp")
	mk := func(lane string) domain.SessionID {
		rec, err := s.CreateSession(ctx, sampleRecord("tp"))
		if err != nil {
			t.Fatal(err)
		}
		if lane != "" {
			if err := s.SetSessionLane(ctx, rec.ID, domain.SessionLane{LaneItemID: lane}); err != nil {
				t.Fatal(err)
			}
		}
		return rec.ID
	}
	orch, wa, wb := mk("lane-a"), mk("lane-a"), mk("lane-b")
	now := time.Now().UTC().Truncate(time.Second)
	for id, worker := range map[string]domain.SessionID{"ra": wa, "rb": wb} {
		if _, err := s.CreateReport(ctx, domain.ReportRecord{ID: id, SessionID: worker, ProjectID: "tp", Message: id, CreatedAt: now, DeliveryState: domain.ReportPending, AvailableAt: now, RepeatCount: 1}); err != nil {
			t.Fatal(err)
		}
	}
	// A batch already claimed for delivery to the dying orchestrator moves too.
	if claimed, err := s.ClaimPendingReportBatch(ctx, "tp", "lane-a", "tok-claimed", now); err != nil || len(claimed) != 1 {
		t.Fatalf("claim lane-a: %v %v", claimed, err)
	}
	var lastSeq int64
	if evs, err := s.EventsAfter(ctx, 0, 1000); err == nil && len(evs) > 0 {
		lastSeq = evs[len(evs)-1].Seq
	}
	if err := s.EndLane(ctx, "tp", "lane-a", orch); err == nil {
		t.Fatal("EndLane deleted a session that is not terminated")
	}
	rec, _, err := s.GetSession(ctx, orch)
	if err != nil {
		t.Fatal(err)
	}
	rec.IsTerminated = true
	if err := s.UpdateSession(ctx, rec); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	if err := s.EndLane(ctx, "tp", "lane-a", orch); err != nil {
		t.Fatalf("EndLane: %v", err)
	}
	if _, found, err := s.GetSession(ctx, orch); err != nil || found {
		t.Fatalf("orchestrator row found=%v err=%v, want it gone", found, err)
	}
	if got, _ := s.SessionLane(ctx, wa); got.LaneItemID != "" {
		t.Errorf("worker still in lane %q", got.LaneItemID)
	}
	if got, _ := s.SessionLane(ctx, wb); got.LaneItemID != "lane-b" {
		t.Errorf("another lane's worker moved to %q", got.LaneItemID)
	}
	if n, err := s.ReleaseReportBatch(ctx, "tp", "tok-claimed", now, "released"); err != nil || n != 1 {
		t.Fatalf("release claimed batch = %d %v", n, err)
	}
	got, err := s.ClaimPendingReportBatch(ctx, "tp", "", "tok", now)
	if err != nil || len(got) != 1 || got[0].ID != "ra" {
		t.Fatalf("default lane claim = %+v err=%v, want the moved (formerly claimed) report ra", got, err)
	}
	evs, err := s.EventsAfter(ctx, lastSeq, 1000)
	if err != nil {
		t.Fatal(err)
	}
	removed := false
	for _, e := range evs {
		if e.Type == cdc.EventSessionUpdated && e.SessionID == "" && e.ProjectID == "tp" && strings.Contains(string(e.Payload), string(orch)) {
			removed = true
		}
	}
	if !removed {
		t.Fatalf("no project-level session_updated event naming %s: %+v", orch, evs)
	}
}

// BacklogRefsByID resolves several lane items in one read and omits unknown ids.
func TestBacklogRefsByIDBatchesAndOmitsUnknown(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "p1")
	a := addBacklog(t, s, "p1", "a", domain.BacklogOpen, "")
	b := addBacklog(t, s, "p1", "b", domain.BacklogOpen, "")
	got, err := s.BacklogRefsByID(ctx, []string{a.ID, b.ID, "gone"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[a.ID] != a.Ref() || got[b.ID] != b.Ref() {
		t.Fatalf("refs = %v, want %s and %s only", got, a.Ref(), b.Ref())
	}
	if got, err = s.BacklogRefsByID(ctx, nil); err != nil || len(got) != 0 {
		t.Fatalf("empty ids = %v, %v", got, err)
	}
}
