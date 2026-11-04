package report

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// laneCountingStore reports several pending lanes and counts the per-tick
// session and lane reads.
type laneCountingStore struct {
	*coordinatorStore
	pending       []string
	laneListCalls int
	laneGetCalls  int
}

func (s *laneCountingStore) SessionLane(context.Context, domain.SessionID) (domain.SessionLane, error) {
	s.laneGetCalls++
	return domain.SessionLane{}, nil
}

func (s *laneCountingStore) ListSessionLanes(context.Context, domain.ProjectID) (map[domain.SessionID]string, error) {
	s.laneListCalls++
	return map[domain.SessionID]string{"orch-a": "a", "orch-b": "b"}, nil
}

func (s *laneCountingStore) ListPendingReportLanes(context.Context, domain.ProjectID, time.Time) ([]string, error) {
	return s.pending, nil
}

func TestDeliveryTickReadsRosterOncePerProjectNotPerLane(t *testing.T) {
	now := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)
	base := newCoordinatorStore(now)
	base.sessions = []domain.SessionRecord{
		{ID: "orch-a", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat},
		{ID: "orch-b", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat},
		{ID: "orch-default", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat},
	}
	base.add(reportAt("one", "worker", now, now, "hello"))
	store := &laneCountingStore{coordinatorStore: base, pending: []string{"a", "b", "c", ""}}
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: &coordinatorDelivery{}, Now: func() time.Time { return now }, NewToken: func() string { return "claim" }})

	if _, err := c.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := base.sessionLists["p"]; got != 1 {
		t.Fatalf("ListSessions ran %d times for 4 lanes, want 1", got)
	}
	if store.laneListCalls != 1 {
		t.Fatalf("ListSessionLanes ran %d times for 4 lanes, want 1", store.laneListCalls)
	}
	if store.laneGetCalls != 0 {
		t.Fatalf("SessionLane ran %d times in a tick, want 0", store.laneGetCalls)
	}
}
