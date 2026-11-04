package backlog_test

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/service/backlog"
)

func evalOn(on bool) func(*backlog.PlanDeps) {
	return func(d *backlog.PlanDeps) {
		d.Eval = func(context.Context, domain.ProjectID) bool { return on }
	}
}

// On an eval-marked project the orchestrator is the owner's proxy: it approves
// without the owner's chat message. A real project keeps the guard.
func TestPlanApprove_EvalProjectOrchestratorNeedsNoOwnerMessage(t *testing.T) {
	e := newPlanEnvWith(t, true, evalOn(true))
	if _, err := e.submitWith(goodSubmit()); err != nil {
		t.Fatal(err)
	}
	got, err := e.plans.Approve(e.ctx, "p1", planOrch, e.item.Ref(), backlog.PlanApproveInput{})
	if err != nil || got.Status != domain.PlanApproved {
		t.Fatalf("Approve on an eval project = %+v, %v; want approved", got, err)
	}
	if got.OwnerMessageID != "" || got.ApprovedBy == "" {
		t.Fatalf("approved by %q with message %q", got.ApprovedBy, got.OwnerMessageID)
	}
}

func TestPlanApprove_RealProjectKeepsOwnerMessageGuard(t *testing.T) {
	for name, tune := range map[string]func(*backlog.PlanDeps){"unmarked": evalOn(false), "unwired": nil} {
		t.Run(name, func(t *testing.T) {
			e := newPlanEnvWith(t, true, tune)
			if _, err := e.submitWith(goodSubmit()); err != nil {
				t.Fatal(err)
			}
			if _, err := e.plans.Approve(e.ctx, "p1", planOrch, e.item.Ref(), backlog.PlanApproveInput{}); apiCode(t, err) != "PLAN_OWNER_MESSAGE_REQUIRED" {
				t.Fatalf("Approve without a message: err = %v, want PLAN_OWNER_MESSAGE_REQUIRED", err)
			}
		})
	}
}
