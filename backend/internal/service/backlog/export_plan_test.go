package backlog

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// PlanSpawnContextForTest exposes the plan section builder to the external tests.
func PlanSpawnContextForTest(p domain.BacklogPlan, ref string) string {
	return planSpawnContext(p, ref)
}

// PlannedPathsChangedForTest exposes the planned-path matcher.
func PlannedPathsChangedForTest(split []domain.PlanPR, changed []string) bool {
	return plannedPathsChanged(split, changed)
}

// UndoPlanChildrenForTest exposes the approval rollback.
func UndoPlanChildrenForTest(ctx context.Context, s *Service, parent domain.BacklogItem, ids []string) {
	s.undoPlanChildren(ctx, parent, ids)
}
