package backlog

import (
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// RunStartInput asks for a run. Refs is the item refs for refs and the parent
// ref for parent; it is ignored for all.
type RunStartInput struct {
	Scope domain.BacklogScope
	Kind  domain.BacklogRunSelection
	Refs  []string
}

// RunPlanItem is one planned item.
type RunPlanItem struct {
	Ref   string
	Model string
}

// RunPlanInput is the orchestrator's plan: lanes of items run in parallel, the
// items inside a lane in order.
type RunPlanInput struct {
	Lanes [][]RunPlanItem
}

// RunItemUpdate changes one planned item's progress.
type RunItemUpdate struct {
	Status   domain.BacklogRunItemStatus
	Reason   string
	Question string
}

// RunPauseInput pauses a run, usually at the Claude usage limit.
type RunPauseInput struct {
	Reason   string
	ResumeAt *time.Time
}

// ItemUsage is what an item's linked sessions used. Elapsed is wall-clock from
// the first session's start to the item's close (or now), not active time.
type ItemUsage struct {
	CostNanos *int64
	// CostComplete is false when some usage events were unpriced.
	CostComplete   bool
	ElapsedSeconds *int64
}

// RunItemView is a planned item with presentation extras.
type RunItemView struct {
	domain.BacklogRunItem
	// DisplayStatus is the stored status unless the item itself says better:
	// a done item has landed and a held one is held.
	DisplayStatus domain.BacklogRunItemStatus
	// PlanStatus is the status of the item's plan (its own, else its parent's); empty without one.
	PlanStatus domain.PlanStatus
	Usage      ItemUsage
}

// RunView is a run with its plan, per-status counts and summed usage.
type RunView struct {
	domain.BacklogRun
	Items  []RunItemView
	Counts map[domain.BacklogRunItemStatus]int
	Usage  ItemUsage
}
