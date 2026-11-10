package backlog

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// Backlog runs and plans (B205). A run asks for a plan for each item that needs
// one; the item waits as needs_you (reason plan_pending) until its own plan is
// approved and then queues like any other. There is no run-wide gate: every item
// follows its own plan, and planners respect the run's worker cap.

// planMode is the planning mode a run follows. Always plans every item without
// a plan; auto plans none by itself (the orchestrator decides, so only items it
// already marked with "Plan now" wait for a plan); off plans none. Disabled
// when the planner switch is off.
func (s *RunService) planMode(ctx context.Context, projectID domain.ProjectID) domain.PlanningMode {
	if !s.plans.Enabled() {
		return domain.PlanningOff
	}
	return s.plans.Mode(ctx, projectID)
}

// approvedPlanFor reports whether the item is covered by an approved plan: its
// own, or its parent's (children made from a PR split share the parent's plan).
func (s *RunService) approvedPlanFor(ctx context.Context, item domain.BacklogItem) (bool, error) {
	for _, id := range []string{item.ID, item.ParentID} {
		if id == "" {
			continue
		}
		pl, ok, err := s.plans.Current(ctx, id)
		if err != nil {
			return false, err
		}
		if ok && pl.Status == domain.PlanApproved {
			return true, nil
		}
	}
	return false, nil
}

// requestPlans starts a planner for each waiting run item that has no approved
// plan and no open one, as far as the worker cap allows. Items it cannot start
// yet stay queued with reason plan_pending and get a planner when a slot frees.
func (s *RunService) requestPlans(ctx context.Context, run domain.BacklogRun) {
	// One pass per run at a time keeps the slot count exact. A call that arrives
	// while a pass runs (another goroutine, or the observer re-entering when a
	// planner fails to start) only asks for another pass, which the running
	// one performs after it finishes. No lock is held across Start.
	g, _ := s.gates.LoadOrStore(run.ID, &planGate{})
	gate, _ := g.(*planGate)
	gate.mu.Lock()
	if gate.running {
		gate.again = true
		gate.mu.Unlock()
		return
	}
	gate.running = true
	gate.mu.Unlock()
	for {
		s.requestPlansOnce(ctx, run)
		gate.mu.Lock()
		if !gate.again {
			gate.running = false
			gate.mu.Unlock()
			return
		}
		gate.again = false
		gate.mu.Unlock()
	}
}

type planGate struct {
	mu             sync.Mutex
	running, again bool
}

func (s *RunService) requestPlansOnce(ctx context.Context, run domain.BacklogRun) {
	if s.plans == nil || run.Status != domain.BacklogRunRunning {
		return
	}
	mode := s.planMode(ctx, run.ProjectID)
	if mode == domain.PlanningOff {
		return
	}
	items, err := s.store.ListBacklogRunItems(ctx, run.ID)
	if err != nil {
		slog.Warn("backlog run: list items for planning", "run", run.Ref(), "error", err)
		return
	}
	type waiting struct {
		run  domain.BacklogRunItem
		item domain.BacklogItem
	}
	var todo []waiting
	slots := domain.MaxBacklogRunWorkers
	for _, it := range items {
		if it.Status.InFlight() {
			slots--
			continue
		}
		if !waitsForPlan(it) {
			continue
		}
		full, ok, err := s.store.GetBacklogItem(ctx, it.ItemID)
		if err != nil || !ok || full.State != domain.BacklogOpen {
			continue
		}
		if approved, err := s.approvedPlanFor(ctx, full); err != nil || approved {
			continue
		}
		cur, has, err := s.plans.Current(ctx, full.ID)
		if err != nil {
			continue
		}
		switch {
		case has && cur.Status.Open():
			slots-- // its planner holds a slot
			s.markPlanPending(ctx, run, it.ItemID, domain.BacklogRunItemNeedsYou, planQuestion(full.Ref(), cur.Status))
		case has && cur.Status == domain.PlanFailed:
			s.markPlanPending(ctx, run, it.ItemID, domain.BacklogRunItemNeedsYou, failedQuestion(full.Ref(), cur))
		case mode == domain.PlanningAlways && it.Reason != domain.BacklogRunReasonPlanCancelled: // a cancelled plan is not restarted by itself
			todo = append(todo, waiting{it, full})
		}
	}
	for _, w := range todo {
		if slots <= 0 {
			s.markPlanPending(ctx, run, w.run.ItemID, domain.BacklogRunItemQueued, "")
			continue
		}
		if _, _, err := s.plans.Start(ctx, run.ProjectID, Actor{Kind: domain.BacklogOriginOrchestrator, SessionID: "run:" + run.Ref()}, w.item.Ref()); err != nil {
			slog.Warn("backlog run: start planner", "run", run.Ref(), "item", w.item.Ref(), "error", err)
			s.markPlanPending(ctx, run, w.run.ItemID, domain.BacklogRunItemNeedsYou,
				fmt.Sprintf("The planner for %s could not start (%s). Run `tallpine plan start %s` to try again.", w.item.Ref(), cleanMsg(err), w.item.Ref()))
			continue
		}
		slots--
	}
}

// waitsForPlan is true for a run item that has not started and is not settled
// by something other than its plan.
func waitsForPlan(it domain.BacklogRunItem) bool {
	return it.Status == domain.BacklogRunItemQueued ||
		(it.Status == domain.BacklogRunItemNeedsYou && (it.Reason == domain.BacklogRunReasonPlanPending || it.Reason == domain.BacklogRunReasonPlanCancelled))
}

func failedQuestion(ref string, pl domain.BacklogPlan) string {
	return fmt.Sprintf("The plan for %s failed (%s). Plan it again from its card or run `tallpine plan start %s`.", ref, pl.Failure, ref)
}

// planQuestion words what the owner is waiting on for a plan status.
func planQuestion(ref string, st domain.PlanStatus) string {
	switch st {
	case domain.PlanAwaitingAnswers:
		return fmt.Sprintf("Answer the planner's questions for %s.", ref)
	case domain.PlanAwaitingApproval:
		return fmt.Sprintf("Review and approve the plan for %s.", ref)
	}
	return fmt.Sprintf("The plan for %s is being drafted; it will ask for you when it is ready.", ref)
}

// markPlanPending parks a run item that waits for its plan. It only touches an
// item that has not started: queued, or already waiting for a plan.
func (s *RunService) markPlanPending(ctx context.Context, run domain.BacklogRun, itemID string, status domain.BacklogRunItemStatus, question string) {
	s.setWaiting(ctx, run, itemID, func(it *domain.BacklogRunItem) {
		it.Status, it.Reason, it.Question = status, domain.BacklogRunReasonPlanPending, question
	})
}

func (s *RunService) setWaiting(ctx context.Context, run domain.BacklogRun, itemID string, apply func(*domain.BacklogRunItem)) {
	_, _, err := s.store.UpdateBacklogRunState(ctx, run.ID, s.now().UTC(), func(_ *domain.BacklogRun, items []domain.BacklogRunItem, _ int) error {
		for i := range items {
			if items[i].ItemID == itemID && waitsForPlan(items[i]) {
				apply(&items[i])
			}
		}
		return nil
	})
	if err != nil {
		slog.Warn("backlog run: record plan status", "run", run.Ref(), "item", itemID, "error", err)
	}
}

// onPlanChange hears every plan status change. For an item in an open run it
// keeps the run item's wait in step with the plan, and when a plan approves,
// fails or is replaced it frees a planner slot for the next waiting item.
func (s *RunService) onPlanChange(ctx context.Context, plan domain.BacklogPlan) {
	if !s.enabled || s.plans == nil || plan.Status == domain.PlanSuperseded {
		return
	}
	runs, err := s.store.ListBacklogRuns(ctx, plan.ProjectID, true, 1)
	if err != nil || len(runs) == 0 {
		return
	}
	run := runs[0]
	items, err := s.store.ListBacklogRunItems(ctx, run.ID)
	if err != nil {
		return
	}
	for _, it := range items {
		if it.ItemID != plan.ItemID {
			continue
		}
		switch plan.Status {
		case domain.PlanApproved:
			s.onPlanApproved(ctx, run, it.ItemID)
		case domain.PlanFailed:
			s.markPlanPending(ctx, run, it.ItemID, domain.BacklogRunItemNeedsYou, failedQuestion(it.ItemRef(), plan))
			s.requestPlans(ctx, run)
		default:
			s.markPlanPending(ctx, run, it.ItemID, domain.BacklogRunItemNeedsYou, planQuestion(it.ItemRef(), plan.Status))
		}
		return
	}
}

// onPlanCancelled parks the run item of a plan the owner cancelled, with an
// accurate question, and frees the planner slot for the next waiting item.
func (s *RunService) onPlanCancelled(ctx context.Context, plan domain.BacklogPlan) {
	if !s.enabled || s.plans == nil {
		return
	}
	runs, err := s.store.ListBacklogRuns(ctx, plan.ProjectID, true, 1)
	if err != nil || len(runs) == 0 {
		return
	}
	run := runs[0]
	items, err := s.store.ListBacklogRunItems(ctx, run.ID)
	if err != nil {
		return
	}
	for _, it := range items {
		if it.ItemID != plan.ItemID {
			continue
		}
		// Park the item and settle in one write: it may have been the last unsettled one.
		now := s.now().UTC()
		updated, _, err := s.store.UpdateBacklogRunState(ctx, run.ID, now, func(r *domain.BacklogRun, its []domain.BacklogRunItem, _ int) error {
			for i := range its {
				if its[i].ItemID == plan.ItemID && waitsForPlan(its[i]) {
					its[i].Status, its[i].Reason = domain.BacklogRunItemNeedsYou, domain.BacklogRunReasonPlanCancelled
					its[i].Question = fmt.Sprintf("The plan for %s was cancelled. Plan it again from its backlog item or run `tallpine plan start %s`, or skip it.", its[i].ItemRef(), its[i].ItemRef())
				}
			}
			settle(r, its, now)
			return nil
		})
		if err != nil {
			slog.Warn("backlog run: park cancelled plan", "run", run.Ref(), "item", it.ItemRef(), "error", err)
			return
		}
		s.requestPlans(ctx, updated)
		return
	}
}

// onPlanApproved releases an item that waited for its plan: it queues like any
// other run item, and the freed planner slot goes to the next one. An item the
// plan split into children stays queued so the orchestrator can swap in the
// children when it re-saves the run plan.
func (s *RunService) onPlanApproved(ctx context.Context, run domain.BacklogRun, itemID string) {
	s.setWaiting(ctx, run, itemID, func(it *domain.BacklogRunItem) {
		it.Status, it.Reason, it.Question = domain.BacklogRunItemQueued, "", ""
	})
	s.replaceWithChildren(ctx, run, itemID)
	s.requestPlans(ctx, run)
}

// replaceWithChildren swaps a run item that the approved plan split into
// children for those children: same lane, in plan order, right where the parent
// stood, so the blocked_by chain between them and the lane order hold. They are
// queued and covered by the parent's plan; the worker cap still applies when
// the orchestrator starts them.
func (s *RunService) replaceWithChildren(ctx context.Context, run domain.BacklogRun, parentID string) {
	kids, err := s.store.ListBacklogItems(ctx, run.ProjectID, store.BacklogFilter{ParentID: parentID, State: domain.BacklogOpen, Limit: domain.MaxBacklogListLimit})
	if err != nil || len(kids) == 0 {
		return
	}
	// The run's items are read inside the save's transaction, so an item added
	// or moved concurrently is kept and a run that closed meanwhile is left alone.
	_, err = s.store.SaveBacklogRunPlanFrom(ctx, run.ID, s.now().UTC(), func(r *domain.BacklogRun, items []domain.BacklogRunItem) ([]store.BacklogRunPlanItem, error) {
		if !r.Status.Open() {
			return nil, errNoReplace
		}
		var parent *domain.BacklogRunItem
		inRun := map[string]bool{}
		for i := range items {
			inRun[items[i].ItemID] = true
			if items[i].ItemID == parentID {
				parent = &items[i]
			}
		}
		if parent == nil || parent.Status != domain.BacklogRunItemQueued || parent.Attempts > 0 {
			return nil, errNoReplace
		}
		var fresh []domain.BacklogItem
		for _, k := range kids {
			if !inRun[k.ID] {
				fresh = append(fresh, k)
			}
		}
		if len(fresh) == 0 {
			return nil, errNoReplace
		}
		plan := make([]store.BacklogRunPlanItem, 0, len(items)+len(fresh))
		for _, it := range items {
			switch {
			case it.ItemID == parentID:
				continue
			case it.Lane == parent.Lane && it.Position > parent.Position:
				it.Position += len(fresh) - 1
			}
			plan = append(plan, store.BacklogRunPlanItem{ItemID: it.ItemID, Lane: it.Lane, Position: it.Position, Model: it.Model})
		}
		for i, k := range fresh {
			plan = append(plan, store.BacklogRunPlanItem{ItemID: k.ID, Lane: parent.Lane, Position: parent.Position + i, Model: parent.Model})
		}
		return plan, nil
	}, func(*domain.BacklogRun) error { return nil })
	if err != nil && !errors.Is(err, errNoReplace) {
		slog.Warn("backlog run: replace split item with its children", "run", run.Ref(), "item", parentID, "error", err)
	}
}

// errNoReplace aborts replaceWithChildren's save when there is nothing to swap.
var errNoReplace = errors.New("nothing to replace")

// planStatusFor is the status of the item's current plan, "" when it has none.
func (s *RunService) planStatusFor(ctx context.Context, item domain.BacklogItem) domain.PlanStatus {
	if s.plans == nil || !s.plans.Enabled() {
		return ""
	}
	for _, id := range []string{item.ID, item.ParentID} {
		if id == "" {
			continue
		}
		if pl, ok, err := s.plans.Current(ctx, id); err == nil && ok {
			return pl.Status
		}
	}
	return ""
}

// checkPlanned refuses to start an item whose plan is still ahead: its own
// plan is open or failed, or the mode is always and nothing approved covers it.
// It reads the plan itself, so rewriting the item's status or reason cannot
// skip the wait.
func (s *RunService) checkPlanned(ctx context.Context, item domain.BacklogItem) error {
	if s.plans == nil {
		return nil
	}
	mode := s.planMode(ctx, item.ProjectID)
	if mode == domain.PlanningOff {
		return nil
	}
	approved, err := s.approvedPlanFor(ctx, item)
	if err != nil || approved {
		return err
	}
	cur, has, err := s.plans.Current(ctx, item.ID)
	if err != nil {
		return err
	}
	if (has && (cur.Status.Open() || cur.Status == domain.PlanFailed)) || mode == domain.PlanningAlways {
		return transitionError("%s waits for its plan to be approved", item.Ref())
	}
	return nil
}
