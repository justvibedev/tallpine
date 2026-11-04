package backlog

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// view assembles a run with its plan, display statuses and usage.
func (s *RunService) view(ctx context.Context, run domain.BacklogRun) (RunView, error) {
	items, err := s.store.ListBacklogRunItems(ctx, run.ID)
	if err != nil {
		return RunView{}, err
	}
	out := RunView{BacklogRun: run, Items: make([]RunItemView, 0, len(items)), Counts: map[domain.BacklogRunItemStatus]int{}}
	for _, it := range items {
		full, ok, err := s.store.GetBacklogItem(ctx, it.ItemID)
		if err != nil {
			return RunView{}, err
		}
		var usage ItemUsage
		var planStatus domain.PlanStatus
		if ok {
			planStatus = s.planStatusFor(ctx, full)
			if usage, err = s.usageFor(ctx, full); err != nil {
				return RunView{}, err
			}
		}
		display := displayStatus(it)
		out.Counts[display]++
		out.Items = append(out.Items, RunItemView{BacklogRunItem: it, DisplayStatus: display, PlanStatus: planStatus, Usage: usage})
	}
	total, err := s.runUsage(ctx, run)
	if err != nil {
		return RunView{}, err
	}
	out.Usage = total
	return out, nil
}

// displayStatus lets the item itself overrule what the orchestrator last wrote:
// lifecycle closes a merged item as done, and an owner may hold or drop it at
// any time, so a stored "active" can be stale.
func displayStatus(it domain.BacklogRunItem) domain.BacklogRunItemStatus {
	switch {
	case it.ItemState == domain.BacklogDone:
		return domain.BacklogRunItemLanded
	case it.ItemState == domain.BacklogDropped:
		return domain.BacklogRunItemHeld
	case it.ItemHold != "" && !it.Status.InFlight() && it.Status != domain.BacklogRunItemLanded:
		return domain.BacklogRunItemHeld
	}
	return it.Status
}
