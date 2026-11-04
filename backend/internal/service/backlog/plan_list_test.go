package backlog_test

import (
	"testing"

	"tallpine.eu/tallpine/backend/internal/service/backlog"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestPlanListOpen_OpenAndFailedOnly(t *testing.T) {
	e := newPlanEnv(t, true)
	if got, err := e.plans.ListOpen(e.ctx, "p1"); err != nil || len(got) != 0 {
		t.Fatalf("no plan: %v, %v", got, err)
	}
	p := e.toApproval()
	got, err := e.plans.ListOpen(e.ctx, "p1")
	if err != nil || len(got) != 1 || got[0].Plan.ID != p.ID || got[0].ItemRef != e.item.Ref() || got[0].ItemTitle != e.item.Title {
		t.Fatalf("awaiting approval plan not listed with its item: %+v, %v", got, err)
	}
	if _, err := e.plans.Approve(e.ctx, "p1", owner, e.item.Ref(), backlog.PlanApproveInput{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.plans.ListOpen(e.ctx, "p1"); len(got) != 0 {
		t.Fatalf("approved plan still listed: %+v", got)
	}
}

func TestPlanListOpen_FailedListedUntilPlannedAgain(t *testing.T) {
	e := newPlanEnv(t, true)
	p := e.mustStart()
	e.plans.SessionEnded(e.ctx, p.PlannerSessionID)
	got, _ := e.plans.ListOpen(e.ctx, "p1")
	if len(got) != 1 || got[0].Plan.Status != domain.PlanFailed {
		t.Fatalf("failed plan not listed: %+v", got)
	}
	if _, _, err := e.plans.Start(e.ctx, "p1", owner, e.item.Ref()); err != nil {
		t.Fatal(err)
	}
	got, _ = e.plans.ListOpen(e.ctx, "p1")
	if len(got) != 1 || got[0].Plan.Status == domain.PlanFailed {
		t.Fatalf("stale failed plan still listed beside the new one: %+v", got)
	}
}

func TestPlanListOpen_OffWhenSwitchOff(t *testing.T) {
	e := newPlanEnv(t, false)
	if _, err := e.plans.ListOpen(e.ctx, "p1"); err == nil {
		t.Fatal("ListOpen answered while the planner is off")
	}
}
