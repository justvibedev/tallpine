package backlog_test

import (
	"context"
	"sync"
	"testing"

	"tallpine.eu/tallpine/backend/internal/service/backlog"

	"tallpine.eu/tallpine/backend/internal/domain"
)

type planNotifier struct {
	mu      sync.Mutex
	sent    []string
	targets []domain.SessionID
}

func (n *planNotifier) NotifyPlanOrchestrator(_ context.Context, _ domain.ProjectID, target domain.SessionID, msg string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.targets = append(n.targets, target)
	n.sent = append(n.sent, msg)
	return nil
}

func TestPlanNoticesTargetTheOrchestratorThatStartedIt(t *testing.T) {
	e := newPlanEnv(t, true)
	p, created, err := e.plans.Start(e.ctx, "p1", planOrch, e.item.Ref())
	if err != nil || !created {
		t.Fatalf("Start = %v, created %v", err, created)
	}
	if p.NotifySessionID != "orch-1" {
		t.Fatalf("NotifySessionID = %q, want orch-1", p.NotifySessionID)
	}
	if _, err := e.plans.SubmitQuestions(e.ctx, "p1", e.planner(p), e.item.Ref(), goodQuestions()); err != nil {
		t.Fatal(err)
	}
	if len(e.notify.targets) == 0 {
		t.Fatal("no notice sent")
	}
	for _, tg := range e.notify.targets {
		if tg != "orch-1" {
			t.Fatalf("notice target %q, want orch-1", tg)
		}
	}
}

func TestInPlaceReplanRecordsTheOrchestratorThatAskedForIt(t *testing.T) {
	e := inPlaceEnv(t)
	_, sid := e.startInPlace()
	e.submitInPlace(sid, goodSubmit().PRs)
	np, err := e.plans.Replan(e.ctx, "p1", planOrch, e.item.Ref())
	if err != nil {
		t.Fatal(err)
	}
	if np.NotifySessionID != "orch-1" {
		t.Fatalf("NotifySessionID = %q, want orch-1", np.NotifySessionID)
	}
}

func TestSpawnInPlaceRecordsTheSpawningOrchestrator(t *testing.T) {
	e := inPlaceEnv(t)
	ctx := backlog.WithSpawner(e.ctx, planOrch)
	sp, err := e.bl.PrepareSpawnPlan(ctx, "p1", e.item.Ref(), false, false, "")
	if err != nil || sp.PlanID == "" {
		t.Fatalf("PrepareSpawnPlan = %+v, %v", sp, err)
	}
	plan, err := e.plans.Get(e.ctx, "p1", e.item.Ref())
	if err != nil || plan.NotifySessionID != "orch-1" {
		t.Fatalf("plan %+v err %v, want NotifySessionID orch-1", plan, err)
	}
}
