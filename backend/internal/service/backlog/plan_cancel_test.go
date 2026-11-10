package backlog_test

import (
	"context"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/service/backlog"
)

func TestPlan_CancelDropsTheWaitingPlanStopsPlannerAndKeepsItemOpen(t *testing.T) {
	for name, setup := range map[string]func(*planEnv) domain.BacklogPlan{
		"answering": func(e *planEnv) domain.BacklogPlan { return e.mustStartWithQuestions() },
		"review":    func(e *planEnv) domain.BacklogPlan { return e.toApproval() },
	} {
		e := newPlanEnv(t, true)
		old := setup(e)
		got, err := e.plans.Cancel(e.ctx, "p1", owner, e.item.Ref())
		if err != nil || got.Status != domain.PlanSuperseded {
			t.Fatalf("%s: Cancel = %+v, %v", name, got, err)
		}
		if len(e.sessions.terminated) != 1 || e.sessions.terminated[0] != old.PlannerSessionID {
			t.Fatalf("%s: terminated %v, want %q", name, e.sessions.terminated, old.PlannerSessionID)
		}
		if open, _ := e.plans.ListOpen(e.ctx, "p1"); len(open) != 0 {
			t.Fatalf("%s: cancelled plan still listed: %+v", name, open)
		}
		if it, _, _ := e.store.GetBacklogItem(e.ctx, e.item.ID); it.State != domain.BacklogOpen {
			t.Fatalf("%s: item is %s, want open", name, it.State)
		}
		if again, _, err := e.plans.Start(e.ctx, "p1", owner, e.item.Ref()); err != nil || again.ID == old.ID {
			t.Fatalf("%s: plan again = %+v, %v", name, again, err)
		}
	}
	// Nothing to cancel, or an approved plan: refused.
	e := newPlanEnv(t, true)
	if _, err := e.plans.Cancel(e.ctx, "p1", owner, e.item.Ref()); apiCode(t, err) != "PLAN_NOT_OPEN" {
		t.Fatalf("cancel without plan: %v", err)
	}
	e.toApproval()
	if _, err := e.plans.Approve(e.ctx, "p1", owner, e.item.Ref(), backlog.PlanApproveInput{}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.Cancel(e.ctx, "p1", owner, e.item.Ref()); apiCode(t, err) != "PLAN_NOT_OPEN" {
		t.Fatalf("cancel approved: %v", err)
	}
}

func TestPlan_CancelAfterPlanAgainDropsTheOldFailedPlanToo(t *testing.T) {
	e := newPlanEnv(t, true)
	failed := e.mustStart()
	e.plans.SessionEnded(e.ctx, failed.PlannerSessionID) // the planner ended: failed
	again, _, err := e.plans.Start(e.ctx, "p1", owner, e.item.Ref())
	if err != nil || again.ID == failed.ID {
		t.Fatalf("plan again = %+v, %v", again, err)
	}
	if _, err := e.plans.Cancel(e.ctx, "p1", owner, e.item.Ref()); err != nil {
		t.Fatal(err)
	}
	if open, _ := e.plans.ListOpen(e.ctx, "p1"); len(open) != 0 {
		t.Fatalf("old failed card came back: %+v", open)
	}
}

func TestInPlaceCancelStopsTheAuthor(t *testing.T) {
	e := inPlaceEnv(t)
	_, sid := e.startInPlace()
	if _, err := e.plans.Cancel(e.ctx, "p1", owner, e.item.Ref()); err != nil {
		t.Fatal(err)
	}
	if len(e.sessions.terminated) != 1 || e.sessions.terminated[0] != sid {
		t.Fatalf("terminated %v, want the in-place author %q", e.sessions.terminated, sid)
	}
}

func TestRun_CancelledPlanParksTheItemAndIsNotRestarted(t *testing.T) {
	e := newRunPlanEnv(t, domain.PlanningAlways)
	a := e.item(t, "A")
	run := e.started(t, a)
	spawned := len(e.sessions.spawned)
	if _, err := e.plans.Cancel(context.Background(), "p1", owner, a); err != nil {
		t.Fatal(err)
	}
	it := e.runItem(t, run, a)
	if it.Status != domain.BacklogRunItemNeedsYou || it.Reason != domain.BacklogRunReasonPlanCancelled || !strings.Contains(it.Question, "cancelled") {
		t.Fatalf("item = %s/%q/%q, want needs_you plan_cancelled with an accurate question", it.Status, it.Reason, it.Question)
	}
	if len(e.sessions.spawned) != spawned {
		t.Fatalf("always mode restarted the cancelled plan: %d planners, was %d", len(e.sessions.spawned), spawned)
	}
	if v, err := e.runs.Get(context.Background(), "p1", run); err != nil || v.Status != domain.BacklogRunDone {
		t.Fatalf("the cancelled item was the last unsettled one: run = %v, %v, want done", v.Status, err)
	}
}

func TestRun_CancelledPlanKeepsRunOpenWhileAnotherItemWaits(t *testing.T) {
	e := newRunPlanEnv(t, domain.PlanningAlways)
	a, b := e.item(t, "A"), e.item(t, "B")
	run := e.started(t, a, b)
	if _, err := e.plans.Cancel(context.Background(), "p1", owner, a); err != nil {
		t.Fatal(err)
	}
	if v, err := e.runs.Get(context.Background(), "p1", run); err != nil || v.Status != domain.BacklogRunRunning {
		t.Fatalf("run = %v, %v, want running while B still plans", v.Status, err)
	}
}

func TestRun_StoppingTheAuthorOnCancelDoesNotRestartThePlan(t *testing.T) {
	e := newRunPlanEnv(t, domain.PlanningAlways)
	a, b := e.item(t, "A"), e.item(t, "B")
	run := e.started(t, a, b)
	spawned := len(e.sessions.spawned)
	// The kill sets off a run pass (any item write ends in requestPlans).
	e.sessions.onTerminate = func() {
		held := backlog.RunItemUpdate{Status: domain.BacklogRunItemHeld, Reason: "waiting"}
		if _, err := e.runs.SetItem(context.Background(), "p1", orch, run, b, held); err != nil {
			t.Errorf("hook: %v", err)
		}
	}
	if _, err := e.plans.Cancel(context.Background(), "p1", owner, a); err != nil {
		t.Fatal(err)
	}
	if len(e.sessions.terminated) == 0 {
		t.Fatal("the planner was not stopped, so the test proves nothing")
	}
	if len(e.sessions.spawned) != spawned {
		t.Fatalf("a run pass during the kill restarted the cancelled plan: %d planners, was %d", len(e.sessions.spawned), spawned)
	}
}

func TestRun_CancelledPlanItemCannotBeStartedUnplanned(t *testing.T) {
	e := newRunPlanEnv(t, domain.PlanningAuto)
	a, b := e.item(t, "A"), e.item(t, "B")
	run := e.started(t, a, b)
	if _, _, err := e.plans.Start(context.Background(), "p1", owner, a); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.Cancel(context.Background(), "p1", owner, a); err != nil {
		t.Fatal(err)
	}
	if _, err := e.runs.SetItem(context.Background(), "p1", orch, run, a, backlog.RunItemUpdate{Status: domain.BacklogRunItemActive}); err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("starting a cancelled-plan item: err = %v, want a refusal that names the cancel", err)
	}
}
