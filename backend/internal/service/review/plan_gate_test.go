package review

import (
	"context"
	"errors"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	reviewcore "tallpine.eu/tallpine/backend/internal/review"
)

// A worker that still authors an unapproved in-place plan cannot trigger a
// review; one without an open plan can (B335).
func TestTriggerRequestedRefusedWhileAnInPlacePlanIsOpen(t *testing.T) {
	svc := New(nil, &fakeStore{})
	triggered := 0
	svc.engineTrigger = func(context.Context, domain.SessionID, reviewcore.TriggerOptions) (reviewcore.TriggerResult, error) {
		triggered++
		return reviewcore.TriggerResult{Created: true}, nil
	}
	open := true
	svc.SetPlanGate(func(context.Context, domain.SessionID) (string, bool) { return "B7", open })
	_, err := svc.TriggerRequested(context.Background(), "w1", TriggerRequest{Source: domain.ReviewTriggerAgent})
	if !errors.Is(err, ErrPlanNotApproved) || !errors.Is(err, ErrConflict) || triggered != 0 {
		t.Fatalf("err = %v, triggered %d", err, triggered)
	}
	open = false
	if _, err := svc.TriggerRequested(context.Background(), "w1", TriggerRequest{Source: domain.ReviewTriggerAgent}); err != nil || triggered != 1 {
		t.Fatalf("approved: err = %v, triggered %d", err, triggered)
	}
}
