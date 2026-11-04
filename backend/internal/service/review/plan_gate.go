package review

import (
	"context"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// ErrPlanNotApproved means the worker authors a plan (plan-in-place, B335) that is
// not approved yet, so it has no work to review.
var ErrPlanNotApproved = fmt.Errorf("%w: plan not approved", ErrConflict)

// planGateFunc reports the item whose in-place plan the worker still authors
// unapproved.
type planGateFunc func(ctx context.Context, workerID domain.SessionID) (itemRef string, open bool)

// SetPlanGate refuses review triggers while the worker authors an unapproved
// plan (plan-in-place). Set once at wiring, before requests arrive.
func (s *Service) SetPlanGate(gate func(ctx context.Context, workerID domain.SessionID) (string, bool)) {
	s.planGate = gate
}

func (s *Service) checkPlanGate(ctx context.Context, workerID domain.SessionID) error {
	if s.planGate == nil {
		return nil
	}
	if ref, open := s.planGate(ctx, workerID); open {
		return fmt.Errorf("%w: the plan for %s is not approved yet; the worker reviews after the owner approves it and it builds", ErrPlanNotApproved, ref)
	}
	return nil
}
