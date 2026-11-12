package backlog_test

import (
	"context"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/service/backlog"
)

// B681: the owner's plan_waiting notification closes once the plan stops
// waiting on them, and never while it still waits.
func TestPlan_ClearsOwnerAlertOnceAnsweredOrApproved(t *testing.T) {
	var events []string
	e := newPlanEnvWith(t, true, func(d *backlog.PlanDeps) {
		d.NotifyOwner = func(_ context.Context, p domain.BacklogPlan, _, _ string) {
			events = append(events, "alert "+string(p.Status))
		}
		d.ClearOwner = func(_ context.Context, p domain.BacklogPlan) {
			events = append(events, "clear "+string(p.Status)+" "+string(p.PlannerSessionID))
		}
	})
	p := e.mustStartWithQuestions()
	if last := events[len(events)-1]; last != "alert awaiting_answers" {
		t.Fatalf("after questions the last event must be the alert, got %v", events)
	}
	if _, err := e.plans.Defaults(e.ctx, "p1", owner, e.item.Ref(), false); err != nil {
		t.Fatal(err)
	}
	if last := events[len(events)-1]; last != "clear writing "+string(p.PlannerSessionID) {
		t.Fatalf("answering must clear the planner's alert, got %v", events)
	}
	if _, err := e.plans.Submit(e.ctx, "p1", e.planner(p), e.item.Ref(), goodSubmit()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.Approve(e.ctx, "p1", owner, e.item.Ref(), backlog.PlanApproveInput{}); err != nil {
		t.Fatal(err)
	}
	if last := events[len(events)-1]; last != "clear approved "+string(p.PlannerSessionID) {
		t.Fatalf("approving must clear the alert, got %v", events)
	}
	for _, ev := range events {
		if strings.HasPrefix(ev, "clear awaiting_") {
			t.Fatalf("a waiting plan must keep its alert, got %v", events)
		}
	}
}
