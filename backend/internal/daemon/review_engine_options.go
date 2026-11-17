package daemon

import (
	"time"

	"tallpine.eu/tallpine/backend/internal/config"
	reviewcore "tallpine.eu/tallpine/backend/internal/review"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

// reviewEngineIdleGrace maps the configured reviewer idle grace onto the
// engine's reading: the engine treats zero as its default, so a configured
// zero (off) becomes negative.
func reviewEngineIdleGrace(cfg config.Config) time.Duration {
	if cfg.ReviewIdleGrace == 0 {
		return -1
	}
	return cfg.ReviewIdleGrace
}

// reviewEnginePlans supplies approved plans to reviewers only when the
// planner is on; nil leaves reviews without plan decisions.
func reviewEnginePlans(cfg config.Config, store *sqlite.Store) reviewcore.PlanSource {
	if !cfg.Planner {
		return nil
	}
	return workerPlanSource{store: store}
}
