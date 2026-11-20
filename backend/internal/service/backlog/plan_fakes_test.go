package backlog_test

import (
	"context"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/service/backlog"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

// fakeSessions records planner spawns and messages; each spawn is a realStore
// session row with role planner, as the session manager would write.
type fakeSessions struct {
	// duringSpawn runs inside SpawnPlanner, as a sweep would during a slow spawn.
	duringSpawn func()
	failSpawn   error
	terminated  []domain.SessionID
	onTerminate func() // runs inside TerminatePlanner, to model a run pass the kill sets off
	mu          sync.Mutex
	store       *sqlite.Store
	spawned     []backlog.PlannerSpawn
	inPlace     []backlog.PlannerSpawn
	sent        []string
	ended       map[domain.SessionID]bool
}

func (f *fakeSessions) SpawnPlanner(ctx context.Context, in backlog.PlannerSpawn) (domain.SessionID, error) {
	if f.duringSpawn != nil {
		f.duringSpawn()
	}
	if f.failSpawn != nil {
		return "", f.failSpawn
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now().UTC()
	rec, err := f.store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: in.ProjectID, Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Role: domain.SessionRolePlanner,
		RequestedOutput: domain.RequestedOutputArtifact, Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: now},
		Metadata: domain.SessionMetadata{Branch: "plan/x", WorkspacePath: "/ws"}, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		return "", err
	}
	f.spawned = append(f.spawned, in)
	return rec.ID, nil
}

func (f *fakeSessions) TerminatePlanner(_ context.Context, id domain.SessionID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.terminated = append(f.terminated, id)
	if f.onTerminate != nil {
		f.mu.Unlock()
		f.onTerminate()
		f.mu.Lock()
	}
	return nil
}

func (f *fakeSessions) PlannerLive(_ context.Context, id domain.SessionID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.ended[id]
}

func (f *fakeSessions) SendToPlanner(_ context.Context, id domain.SessionID, msg string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ended[id] {
		return false, nil
	}
	f.sent = append(f.sent, msg)
	return true, nil
}

// toApproval drives a plan to awaiting_approval.
func (e *planEnv) toApproval() domain.BacklogPlan {
	e.t.Helper()
	p := e.mustStart()
	if _, err := e.plans.SubmitQuestions(e.ctx, "p1", e.planner(p), e.item.Ref(), goodQuestions()); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.plans.Defaults(e.ctx, "p1", owner, e.item.Ref(), false); err != nil {
		e.t.Fatal(err)
	}
	p, err := e.plans.Submit(e.ctx, "p1", e.planner(p), e.item.Ref(), goodSubmit())
	if err != nil {
		e.t.Fatal(err)
	}
	return p
}
