package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	sessionsvc "tallpine.eu/tallpine/backend/internal/service/session"
)

// planLeadSessions is the session access the lane notice needs.
type planLeadSessions = backlogRunSessions

// planLeadNotice tells the project's default (un-laned) orchestrator when a
// plan from a lane waits on the owner, so the main chat knows the lane is
// blocked. It sends once per plan and state; plans of the default orchestrator
// or without a lane send nothing. Failures are logged only.
type planLeadNotice struct {
	sessions planLeadSessions
	items    planLeadItems
	mu       sync.Mutex
	sent     map[string]bool
}

func (n *planLeadNotice) notify(ctx context.Context, plan domain.BacklogPlan, itemRef string) {
	if n == nil || n.sessions == nil {
		return
	}
	key := plan.ID + ":" + string(plan.Status)
	n.mu.Lock()
	done := n.sent[key]
	n.mu.Unlock()
	if done {
		return
	}
	// Planners carry no lane of their own: a plan is in a lane when its
	// item's parent is the lane a live lane orchestrator owns.
	if n.items == nil {
		return
	}
	item, ok, err := n.items.GetBacklogItem(ctx, plan.ItemID)
	if err != nil || !ok {
		return
	}
	// A lane may be a top-level item the lane orchestrator plans itself.
	lane := item.Ref()
	if item.ParentID != "" {
		parent, ok, err := n.items.GetBacklogItem(ctx, item.ParentID)
		if err != nil || !ok {
			return
		}
		lane = parent.Ref()
	}
	active := true
	found, err := n.sessions.List(ctx, sessionsvc.ListFilter{ProjectID: plan.ProjectID, Active: &active, OrchestratorOnly: true})
	if err != nil {
		slog.Warn("could not list orchestrators for a waiting lane plan", "plan", plan.ID, "error", err)
		return
	}
	inLane := false
	for _, s := range found {
		inLane = inLane || s.Lane == lane
	}
	if !inLane {
		return
	}
	var lead domain.Session
	for _, s := range found {
		if s.Lane == "" && (lead.ID == "" || s.CreatedAt.After(lead.CreatedAt)) {
			lead = s
		}
	}
	if lead.ID == "" {
		return
	}
	msg := fmt.Sprintf("[auto] plan_waiting: %s in lane %s is awaiting the owner (%s)", itemRef, lane, plan.Status)
	if err := n.sessions.SendWithOptions(ctx, lead.ID, msg, nil, ports.MessageDeliveryOptions{}); err != nil {
		slog.Warn("could not tell the default orchestrator about a waiting lane plan", "plan", plan.ID, "error", err)
		return
	}
	n.mu.Lock()
	n.sent[key] = true
	n.mu.Unlock()
}
