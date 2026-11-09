package review

import (
	stdctx "context"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// AutoReviewSource is what resolves a live worker's Auto review: the stored
// per-session choice and the app-wide workflow defaults. The sqlite store
// implements it; the daemon asserts that at compile time.
type AutoReviewSource interface {
	SessionAutoReviewOverride(ctx stdctx.Context, id domain.SessionID) (enabled, found bool, err error)
	WorkflowDefaults(ctx stdctx.Context) (domain.WorkflowDefaults, error)
}

// AutoReviewActive is the one place that decides whether Auto review applies to
// a live worker (B389): its deliberate per-session choice if it has one, else
// the current project or app setting. The flag copied at spawn is not consulted,
// so switching the setting on or off covers workers spawned earlier. src may be
// any store; one that is not an AutoReviewSource contributes no override or app
// default. found says whether the project record exists.
func AutoReviewActive(ctx stdctx.Context, src any, worker domain.SessionRecord, proj domain.ProjectRecord, found bool) bool {
	var override *bool
	if s, ok := src.(interface {
		SessionAutoReviewOverride(stdctx.Context, domain.SessionID) (bool, bool, error)
	}); ok {
		v, has, err := s.SessionAutoReviewOverride(ctx, worker.ID)
		if err != nil {
			return false // fail closed: a read error must not undo an explicit disable
		}
		if has {
			override = &v
		}
	}
	if override != nil {
		return *override
	}
	if !found {
		return worker.AutoReviewEnabled
	}
	var app domain.WorkflowDefaults
	if s, ok := src.(interface {
		WorkflowDefaults(stdctx.Context) (domain.WorkflowDefaults, error)
	}); ok {
		if d, err := s.WorkflowDefaults(ctx); err == nil {
			app = d
		}
	}
	return proj.Config.EffectiveAutoReview(app)
}
