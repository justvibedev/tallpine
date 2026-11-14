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
	sp, err := e.bl.PrepareSpawnPlan(ctx, "p1", e.item.Ref(), false, false, false, "")
	if err != nil || sp.PlanID == "" {
		t.Fatalf("PrepareSpawnPlan = %+v, %v", sp, err)
	}
	plan, err := e.plans.Get(e.ctx, "p1", e.item.Ref())
	if err != nil || plan.NotifySessionID != "orch-1" {
		t.Fatalf("plan %+v err %v, want NotifySessionID orch-1", plan, err)
	}
}

// B696: the planner must be spawned as the starting orchestrator's child so its
// reports follow that orchestrator's lane, not the project's main orchestrator.
func TestPlannerIsSpawnedAsChildOfTheStartingOrchestrator(t *testing.T) {
	e := newPlanEnv(t, true)
	if _, _, err := e.plans.Start(e.ctx, "p1", planOrch, e.item.Ref()); err != nil {
		t.Fatal(err)
	}
	if len(e.sessions.spawned) != 1 || e.sessions.spawned[0].StarterSessionID != "orch-1" {
		t.Fatalf("spawned = %+v, want StarterSessionID orch-1", e.sessions.spawned)
	}
}

func TestPlannerRespawnKeepsTheStartingOrchestrator(t *testing.T) {
	e := newPlanEnv(t, true)
	p, _, err := e.plans.Start(e.ctx, "p1", planOrch, e.item.Ref())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.SubmitQuestions(e.ctx, "p1", e.planner(p), e.item.Ref(), goodQuestions()); err != nil {
		t.Fatal(err)
	}
	e.sessions.ended[p.PlannerSessionID] = true
	if _, err := e.plans.Defaults(e.ctx, "p1", owner, e.item.Ref(), false); err != nil {
		t.Fatal(err)
	}
	if len(e.sessions.spawned) != 2 || e.sessions.spawned[1].StarterSessionID != "orch-1" {
		t.Fatalf("respawn = %+v, want StarterSessionID orch-1", e.sessions.spawned)
	}
}

// B696: an in-place author started again by a replan joins the replanning
// orchestrator's lane, like the planner it stands in for.
func TestInPlaceReplanSpawnCarriesTheStartingOrchestrator(t *testing.T) {
	e := inPlaceEnv(t)
	_, sid := e.startInPlace()
	e.sessions.ended[sid] = true
	if _, err := e.plans.Replan(e.ctx, "p1", planOrch, e.item.Ref()); err != nil {
		t.Fatal(err)
	}
	if len(e.sessions.inPlace) != 1 || e.sessions.inPlace[0].StarterSessionID != "orch-1" {
		t.Fatalf("in-place spawns = %+v, want StarterSessionID orch-1", e.sessions.inPlace)
	}
}
