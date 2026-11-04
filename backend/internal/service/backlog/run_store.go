package backlog

import (
	"context"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// RunStore is the run service's persistence surface; the SQLite store satisfies it.
type RunStore interface {
	CreateBacklogRun(ctx context.Context, in domain.BacklogRun) (domain.BacklogRun, error)
	GetBacklogRun(ctx context.Context, projectID domain.ProjectID, seq int64) (domain.BacklogRun, bool, error)
	ListBacklogRuns(ctx context.Context, projectID domain.ProjectID, openOnly bool, limit int) ([]domain.BacklogRun, error)
	ListBacklogRunItems(ctx context.Context, runID string) ([]domain.BacklogRunItem, error)
	SaveBacklogRunPlan(ctx context.Context, runID string, plan []store.BacklogRunPlanItem, now time.Time, mutate func(*domain.BacklogRun) error) (domain.BacklogRun, error)
	SaveBacklogRunPlanFrom(ctx context.Context, runID string, now time.Time, build func(*domain.BacklogRun, []domain.BacklogRunItem) ([]store.BacklogRunPlanItem, error), mutate func(*domain.BacklogRun) error) (domain.BacklogRun, error)
	UpdateBacklogRunState(ctx context.Context, runID string, now time.Time, mutate func(*domain.BacklogRun, []domain.BacklogRunItem, int) error) (domain.BacklogRun, []domain.BacklogRunItem, error)
	BacklogRunUsageFor(ctx context.Context, runID string) (store.BacklogItemUsage, error)
	BacklogItemUsageFor(ctx context.Context, itemID string) (store.BacklogItemUsage, error)
	GetBacklogItem(ctx context.Context, id string) (domain.BacklogItem, bool, error)
	GetBacklogItemByRef(ctx context.Context, projectID domain.ProjectID, seq int64) (domain.BacklogItem, bool, error)
	ListBacklogItems(ctx context.Context, projectID domain.ProjectID, f store.BacklogFilter) ([]domain.BacklogItem, error)
	CountBacklogChildren(ctx context.Context, id string) (int, error)
}
