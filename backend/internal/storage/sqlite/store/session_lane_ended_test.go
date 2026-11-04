package store_test

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// Terminating a lane orchestrator through the store's own write path ends the
// lane: its sessions lose their lane and it is listed as an ended orchestrator.
func TestTerminatingALaneOrchestratorEndsTheLane(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "tp")
	mk := func(kind domain.SessionKind, lane string, parent domain.SessionID) domain.SessionID {
		rec := sampleRecord("tp")
		rec.Kind = kind
		created, err := s.CreateSession(ctx, rec)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SetSessionLane(ctx, created.ID, domain.SessionLane{LaneItemID: lane, ParentSessionID: parent}); err != nil {
			t.Fatal(err)
		}
		return created.ID
	}
	orch := mk(domain.KindOrchestrator, "lane-a", "")
	worker := mk(domain.KindWorker, "lane-a", orch)

	if ended, err := s.EndedLaneOrchestrators(ctx); err != nil || len(ended) != 0 {
		t.Fatalf("ended before = %v err=%v", ended, err)
	}
	rec, _, err := s.GetSession(ctx, orch)
	if err != nil {
		t.Fatal(err)
	}
	rec.IsTerminated = true
	if err := s.UpdateSession(ctx, rec); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	ended, err := s.EndedLaneOrchestrators(ctx)
	if err != nil || len(ended) != 1 || !ended[orch] {
		t.Fatalf("ended = %v err=%v, want %s", ended, err, orch)
	}
	for _, id := range []domain.SessionID{orch, worker} {
		if lane, err := s.SessionLane(ctx, id); err != nil || lane != (domain.SessionLane{}) {
			t.Fatalf("%s lane = %+v err=%v, want none", id, lane, err)
		}
	}
	if lanes, err := s.ListSessionLanes(ctx, "tp"); err != nil || len(lanes) != 0 {
		t.Fatalf("lanes = %v err=%v", lanes, err)
	}
}

// A clean replacement holds the lane: retiring the old orchestrator keeps the
// worker and its pending report in the lane, and the new orchestrator is not
// ended. Released without a live orchestrator (a failed spawn), the lane ends.
func TestHeldLaneSurvivesACleanReplacement(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "tp")
	mk := func(kind domain.SessionKind, parent domain.SessionID) domain.SessionID {
		rec := sampleRecord("tp")
		rec.Kind = kind
		rec.Lane = domain.SessionLane{LaneItemID: "lane-a", ParentSessionID: parent}
		created, err := s.CreateSession(ctx, rec)
		if err != nil {
			t.Fatal(err)
		}
		return created.ID
	}
	terminate := func(id domain.SessionID) {
		rec, _, err := s.GetSession(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		rec.IsTerminated = true
		if err := s.UpdateSession(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	old := mk(domain.KindOrchestrator, "")
	worker := mk(domain.KindWorker, old)
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := s.CreateReport(ctx, domain.ReportRecord{ID: "r1", SessionID: worker, ProjectID: "tp", Message: "hi", CreatedAt: now, DeliveryState: domain.ReportPending, AvailableAt: now, RepeatCount: 1}); err != nil {
		t.Fatal(err)
	}

	if err := s.HoldLane(ctx, "tp", "lane-a"); err != nil {
		t.Fatal(err)
	}
	terminate(old)
	replacement := mk(domain.KindOrchestrator, "")
	if err := s.ReleaseLane(ctx, "tp", "lane-a"); err != nil {
		t.Fatal(err)
	}
	if lane, err := s.SessionLane(ctx, worker); err != nil || lane.LaneItemID != "lane-a" {
		t.Fatalf("worker lane = %+v err=%v, want lane-a", lane, err)
	}
	if lanes, err := s.ListPendingReportLanes(ctx, "tp", now.Add(time.Hour)); err != nil || len(lanes) != 1 || lanes[0] != "lane-a" {
		t.Fatalf("report lanes = %v err=%v, want lane-a", lanes, err)
	}
	if ended, err := s.EndedLaneOrchestrators(ctx); err != nil || len(ended) != 0 {
		t.Fatalf("ended = %v err=%v, want none", ended, err)
	}

	// The replacement then fails: a held lane whose orchestrator died ends on
	// release.
	if err := s.HoldLane(ctx, "tp", "lane-a"); err != nil {
		t.Fatal(err)
	}
	terminate(replacement)
	if lane, _ := s.SessionLane(ctx, worker); lane.LaneItemID != "lane-a" {
		t.Fatal("lane ended while held")
	}
	if err := s.ReleaseLane(ctx, "tp", "lane-a"); err != nil {
		t.Fatal(err)
	}
	if lanes, err := s.ListSessionLanes(ctx, "tp"); err != nil || len(lanes) != 0 {
		t.Fatalf("lanes after failed replacement = %v err=%v", lanes, err)
	}
	if ended, _ := s.EndedLaneOrchestrators(ctx); !ended[old] || !ended[replacement] {
		t.Fatalf("ended = %v, want both orchestrators", ended)
	}
	if lanes, _ := s.ListPendingReportLanes(ctx, "tp", now.Add(time.Hour)); len(lanes) != 1 || lanes[0] != "" {
		t.Fatalf("report lanes = %v, want the default lane", lanes)
	}
}

// A hold left by a crash mid-replacement is released at daemon start, and the
// lane it kept open ends.
func TestReleaseStaleLaneHoldsEndsAbandonedLanes(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "tp")
	rec := sampleRecord("tp")
	rec.Kind = domain.KindOrchestrator
	rec.Lane = domain.SessionLane{LaneItemID: "lane-a"}
	orch, err := s.CreateSession(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.HoldLane(ctx, "tp", "lane-a"); err != nil {
		t.Fatal(err)
	}
	orch.IsTerminated = true
	if err := s.UpdateSession(ctx, orch); err != nil {
		t.Fatal(err)
	}
	if lane, _ := s.SessionLane(ctx, orch.ID); lane.LaneItemID != "lane-a" {
		t.Fatal("held lane ended before the sweep")
	}
	if err := s.ReleaseStaleLaneHolds(ctx); err != nil {
		t.Fatal(err)
	}
	if lanes, err := s.ListSessionLanes(ctx, "tp"); err != nil || len(lanes) != 0 {
		t.Fatalf("lanes = %v err=%v, want the lane ended", lanes, err)
	}
	if ended, _ := s.EndedLaneOrchestrators(ctx); !ended[orch.ID] {
		t.Fatal("orchestrator not recorded as ended")
	}
	// The hold is gone: a later termination in a new lane-a ends it at once.
	rec.Lane = domain.SessionLane{LaneItemID: "lane-a"}
	next, err := s.CreateSession(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}
	next.IsTerminated = true
	if err := s.UpdateSession(ctx, next); err != nil {
		t.Fatal(err)
	}
	if lanes, _ := s.ListSessionLanes(ctx, "tp"); len(lanes) != 0 {
		t.Fatalf("lanes = %v, want none: the hold was not cleared", lanes)
	}
}
