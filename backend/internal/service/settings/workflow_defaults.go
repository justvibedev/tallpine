package settings

import (
	"context"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// workflowDefaultsStore is the optional store ability to save the app-wide
// workflow defaults.
type workflowDefaultsStore interface {
	SetWorkflowDefaults(ctx context.Context, d domain.WorkflowDefaults, now time.Time) error
}

// WorkflowDefaultsUpdate changes any subset of the defaults; nil keeps a value.
type WorkflowDefaultsUpdate struct {
	AutoReview      *bool
	PlanningMode    *domain.PlanningMode
	PlanAutoApprove *bool
	// ReviewerHarness and ReviewerModel set the app-wide default reviewer; an
	// empty harness clears it (and its model).
	ReviewerHarness *domain.ReviewerHarness
	ReviewerModel   *string
}

// WorkflowDefaults reports the app-wide workflow defaults. An unreadable store
// gives the built-in defaults (everything off), so a read failure never turns
// automation on.
func (s *Service) WorkflowDefaults(ctx context.Context) domain.WorkflowDefaults {
	snapshot, err := s.store.GetAppSettings(ctx)
	if err != nil {
		return domain.DefaultWorkflowDefaults()
	}
	return snapshot.WorkflowDefaults.Normalized()
}

// SetWorkflowDefaults changes any subset of the app-wide workflow defaults.
func (s *Service) SetWorkflowDefaults(ctx context.Context, update WorkflowDefaultsUpdate) (Snapshot, error) {
	st, ok := s.store.(workflowDefaultsStore)
	if !ok {
		return Snapshot{}, ports.ErrChatUnsupported
	}
	current, err := s.store.GetAppSettings(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	next := current.WorkflowDefaults.Normalized()
	if update.AutoReview != nil {
		next.AutoReview = *update.AutoReview
	}
	if update.PlanningMode != nil {
		next.PlanningMode = *update.PlanningMode
	}
	if update.PlanAutoApprove != nil {
		next.PlanAutoApprove = *update.PlanAutoApprove
	}
	if update.ReviewerHarness != nil {
		// A model belongs to its harness: a changed harness drops the old model
		// unless the same update names a new one.
		if *update.ReviewerHarness != next.ReviewerHarness && update.ReviewerModel == nil {
			next.ReviewerModel = ""
		}
		next.ReviewerHarness = *update.ReviewerHarness
	}
	if update.ReviewerModel != nil {
		next.ReviewerModel = *update.ReviewerModel
	}
	if next.ReviewerHarness == "" {
		next.ReviewerModel = ""
	}
	if err := st.SetWorkflowDefaults(ctx, next.Normalized(), s.now()); err != nil {
		return Snapshot{}, err
	}
	return s.Get(ctx)
}
