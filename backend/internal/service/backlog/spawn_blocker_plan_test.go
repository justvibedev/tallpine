package backlog_test

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/service/backlog"
)

func TestPrepareSpawn_BlockerOverrideKeepsPlanGate(t *testing.T) {
	e := newPlanEnvWith(t, true, func(d *backlog.PlanDeps) {
		d.Mode = func(context.Context, domain.ProjectID) domain.PlanningMode { return domain.PlanningAlways }
	})
	blocker, err := e.bl.Create(e.ctx, "p1", backlog.Actor{Kind: domain.BacklogOriginHuman}, backlog.CreateInput{Title: "blocker"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.bl.Link(e.ctx, "p1", e.item.Ref(), domain.BacklogLinkBlockedBy, blocker.Ref(), ""); err != nil {
		t.Fatal(err)
	}
	_, err = e.bl.PrepareSpawnPlan(e.ctx, "p1", e.item.Ref(), false, false, false, "separate")
	if apiCode(t, err) != "BACKLOG_ITEM_BLOCKED" {
		t.Fatalf("no override = %v, want BACKLOG_ITEM_BLOCKED", err)
	}
	_, err = e.bl.PrepareSpawnPlan(e.ctx, "p1", e.item.Ref(), false, false, true, "separate")
	if apiCode(t, err) != "PLAN_REQUIRED" {
		t.Fatalf("allowBlocked = %v, want the blocker passed but PLAN_REQUIRED kept", err)
	}
}
