package store_test

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestWorkflowDefaultsRoundTripAndMissingKeys(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	got, err := s.WorkflowDefaults(ctx)
	if err != nil || got != domain.DefaultWorkflowDefaults() {
		t.Fatalf("fresh store: %+v %v, want built-in defaults", got, err)
	}
	want := domain.WorkflowDefaults{AutoReview: true, PlanningMode: domain.PlanningAlways, PlanAutoApprove: true}
	if err := s.SetWorkflowDefaults(ctx, want, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.WorkflowDefaults(ctx); got != want {
		t.Fatalf("round trip: %+v, want %+v", got, want)
	}
	row, err := s.GetAppSettings(ctx)
	if err != nil || row.WorkflowDefaults != want {
		t.Fatalf("GetAppSettings: %+v %v", row.WorkflowDefaults, err)
	}
}
