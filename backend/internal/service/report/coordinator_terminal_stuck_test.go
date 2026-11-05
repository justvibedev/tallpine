package report

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestCoordinatorStuckNeverInterruptsTerminalOrchestrator(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeTUI}}
	stuck := reportAt("stuck-1", "worker", now, now, "cannot continue")
	stuck.State = domain.ReportStuck
	store.add(stuck)
	delivery := &coordinatorDelivery{}
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: delivery, Now: func() time.Time { return now }})
	if _, err := c.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(delivery.interrupts) != 0 {
		t.Fatalf("interrupts = %v", delivery.interrupts)
	}
	if len(delivery.submissions) != 1 {
		t.Fatalf("submissions = %#v", delivery.submissions)
	}
}
