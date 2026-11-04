package backlog

import (
	"context"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
)

// ItemUsage reads what the item's linked sessions have used from the usage
// ledger (Claude, Codex and DeepSeek sessions all record there).
func (s *RunService) ItemUsage(ctx context.Context, projectID domain.ProjectID, ref string) (ItemUsage, error) {
	if err := s.requireEnabled(); err != nil {
		return ItemUsage{}, err
	}
	item, err := s.resolveRunItem(ctx, projectID, ref)
	if err != nil {
		return ItemUsage{}, err
	}
	return s.usageFor(ctx, item)
}

func (s *RunService) usageFor(ctx context.Context, item domain.BacklogItem) (ItemUsage, error) {
	u, err := s.store.BacklogItemUsageFor(ctx, item.ID)
	if err != nil {
		return ItemUsage{}, err
	}
	var out ItemUsage
	if u.Events > 0 {
		cost := u.PricedNanos
		out.CostNanos, out.CostComplete = &cost, u.PricedEvents == u.Events
	}
	if !u.FirstStarted.IsZero() {
		end := s.now().UTC()
		if item.ClosedAt != nil {
			end = *item.ClosedAt
		}
		secs := int64(end.Sub(u.FirstStarted) / time.Second)
		if secs < 0 {
			secs = 0
		}
		out.ElapsedSeconds = &secs
	}
	return out, nil
}

// runUsage totals a run: cost over its distinct worker sessions (one linked to
// two items counts once) and elapsed from the run's own start to its end (or
// now), so parallel lanes do not add up.
func (s *RunService) runUsage(ctx context.Context, run domain.BacklogRun) (ItemUsage, error) {
	u, err := s.store.BacklogRunUsageFor(ctx, run.ID)
	if err != nil {
		return ItemUsage{}, err
	}
	var out ItemUsage
	if u.Events > 0 {
		cost := u.PricedNanos
		out.CostNanos, out.CostComplete = &cost, u.PricedEvents == u.Events
	}
	if run.StartedAt != nil {
		end := s.now().UTC()
		if run.EndedAt != nil {
			end = *run.EndedAt
		}
		secs := int64(end.Sub(*run.StartedAt) / time.Second)
		if secs < 0 {
			secs = 0
		}
		out.ElapsedSeconds = &secs
	}
	return out, nil
}

func (s *RunService) requireEnabled() error {
	if !s.enabled {
		return apierr.NotFound("BACKLOG_RUN_DISABLED", "Backlog run mode is off (TALLPINE_BACKLOG_RUN=off on this daemon)")
	}
	return nil
}
