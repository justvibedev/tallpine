package backlog_test

import (
	"context"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/service/backlog"
)

// submitWith drives a fresh plan to Submit and returns the Submit result.
func (e *planEnv) submitWith(in backlog.PlanSubmitInput) (domain.BacklogPlan, error) {
	e.t.Helper()
	p := e.mustStart()
	if _, err := e.plans.SubmitQuestions(e.ctx, "p1", e.planner(p), e.item.Ref(), backlog.PlanQuestionsInput{}); err != nil {
		e.t.Fatal(err)
	}
	return e.plans.Submit(e.ctx, "p1", e.planner(p), e.item.Ref(), in)
}

func autoOn(on bool) func(*backlog.PlanDeps) {
	return func(d *backlog.PlanDeps) {
		d.AutoApprove = func(context.Context, domain.ProjectID) bool { return on }
	}
}

func TestPlanAutoApprove_ApprovesOnSubmitWithAutoActor(t *testing.T) {
	e := newPlanEnvWith(t, true, autoOn(true))
	got, err := e.submitWith(goodSubmit())
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.PlanApproved || got.ApprovedBy != "auto (project setting)" || got.OwnerMessageID != "" {
		t.Fatalf("plan = %+v, want approved by auto (project setting)", got)
	}
	cur, ok, err := e.plans.Current(e.ctx, e.item.ID)
	if err != nil || !ok || cur.Status != domain.PlanApproved {
		t.Fatalf("stored plan = %+v, %v, %v", cur, ok, err)
	}
	var sawApproved, sawAskOwner bool
	for _, m := range e.notify.sent {
		sawApproved = sawApproved || strings.Contains(m, "is approved")
		sawAskOwner = sawAskOwner || strings.Contains(m, "ready for the owner to approve")
	}
	if !sawApproved || sawAskOwner {
		t.Fatalf("notifications = %q, want the approved notice and no ask-the-owner notice", e.notify.sent)
	}
}

func TestPlanAutoApprove_CreatesChildItemsForTwoPlusPRs(t *testing.T) {
	e := newPlanEnvWith(t, true, autoOn(true))
	got, err := e.submitWith(twoPRSubmit())
	if err != nil || got.Status != domain.PlanApproved {
		t.Fatalf("submit = %+v, %v", got, err)
	}
	if n := len(e.children()); n != 2 {
		t.Fatalf("children = %d, want 2 like a normal approval", n)
	}
}

func TestPlanAutoApprove_OffChangesNothing(t *testing.T) {
	for name, tune := range map[string]func(*backlog.PlanDeps){"unset": nil, "false": autoOn(false)} {
		t.Run(name, func(t *testing.T) {
			e := newPlanEnvWith(t, true, tune)
			got, err := e.submitWith(goodSubmit())
			if err != nil || got.Status != domain.PlanAwaitingApproval || got.ApprovedBy != "" {
				t.Fatalf("plan = %+v, %v, want awaiting approval", got, err)
			}
			if n := len(e.children()); n != 0 {
				t.Fatalf("children = %d, want none", n)
			}
			var asked bool
			for _, m := range e.notify.sent {
				asked = asked || strings.Contains(m, "ready for the owner to approve")
			}
			if !asked {
				t.Fatalf("notifications = %q, want the ask-the-owner notice", e.notify.sent)
			}
		})
	}
}

// An approval is by a project setting only; agents still cannot approve on
// their own through Approve.
func TestPlanAutoApprove_DoesNotLoosenAgentApproval(t *testing.T) {
	e := newPlanEnvWith(t, true, autoOn(false))
	if _, err := e.submitWith(goodSubmit()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.Approve(e.ctx, "p1", planOrch, e.item.Ref(), backlog.PlanApproveInput{}); apiCode(t, err) != "PLAN_OWNER_MESSAGE_REQUIRED" {
		t.Fatalf("orchestrator approve without message: err = %v", err)
	}
}

func TestPlanAutoApprove_SweepApprovesWaitingPlansWhenTurnedOn(t *testing.T) {
	on := false
	e := newPlanEnvWith(t, true, func(d *backlog.PlanDeps) {
		d.AutoApprove = func(context.Context, domain.ProjectID) bool { return on }
	})
	got, err := e.submitWith(goodSubmit())
	if err != nil || got.Status != domain.PlanAwaitingApproval {
		t.Fatalf("submit = %+v, %v, want awaiting approval", got, err)
	}
	e.plans.ApproveWaiting(e.ctx, "p1") // setting still off: nothing happens
	if cur, _, _ := e.plans.Current(e.ctx, e.item.ID); cur.Status != domain.PlanAwaitingApproval {
		t.Fatalf("plan = %+v, want still waiting while off", cur)
	}
	on = true
	e.plans.ApproveWaiting(e.ctx, "p1")
	cur, ok, err := e.plans.Current(e.ctx, e.item.ID)
	if err != nil || !ok || cur.Status != domain.PlanApproved || cur.ApprovedBy != backlog.AutoApprovedBy {
		t.Fatalf("plan = %+v, %v, %v, want approved by auto", cur, ok, err)
	}
}

func TestPlanAutoApprove_HoldsPlanThatNeedsTheOwner(t *testing.T) {
	for _, d := range []string{
		"| q | a |\n|---|---|\n| Q1 | use X (not confirmed by owner) |",
		"| Q1 | Y: overrides owner answer to Q1 |",
		"| Q1 | overriding owner |", "| Q1 | overriding the owner |", "| Q1 | override the owner |",
		"| Q1 | overrode an owner answer |", "| Q1 | unconfirmed by the owner |", "| Q1 | not  confirmed\nby owner |",
	} {
		on := true
		e := newPlanEnvWith(t, true, func(pd *backlog.PlanDeps) {
			pd.AutoApprove = func(context.Context, domain.ProjectID) bool { return on }
		})
		in := goodSubmit()
		in.Decisions = d
		got, err := e.submitWith(in)
		if err != nil || got.Status != domain.PlanAwaitingApproval {
			t.Fatalf("submit = %+v, %v, want awaiting approval for %q", got, err, d)
		}
		e.plans.ApproveWaiting(e.ctx, "p1")
		if cur, _, _ := e.plans.Current(e.ctx, e.item.ID); cur.Status != domain.PlanAwaitingApproval {
			t.Fatalf("sweep approved a plan needing the owner: %+v", cur)
		}
	}
}
