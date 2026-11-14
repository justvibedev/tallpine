package daemon

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	backlogsvc "tallpine.eu/tallpine/backend/internal/service/backlog"
)

// captureSpawns records each spawn config.
type captureSpawns struct {
	planSessions
	cfgs []ports.SpawnConfig
}

func (c *captureSpawns) Spawn(_ context.Context, cfg ports.SpawnConfig) (domain.Session, int, int, error) {
	c.cfgs = append(c.cfgs, cfg)
	return domain.Session{ID: "s1"}, 0, 0, nil
}

// B696: planner and in-place spawns carry the starting orchestrator as parent,
// so the session manager gives them its lane and their reports follow it.
func TestPlannerSpawnsAreChildrenOfTheStarter(t *testing.T) {
	in := backlogsvc.PlannerSpawn{ProjectID: "p1", StarterSessionID: "orch-1"}
	cases := map[string]func(plannerSessions) error{
		"planner":  func(p plannerSessions) error { _, err := p.SpawnPlanner(context.Background(), in); return err },
		"in-place": func(p plannerSessions) error { _, err := p.SpawnInPlace(context.Background(), in); return err },
	}
	for name, run := range cases {
		spawns := &captureSpawns{}
		if err := run(plannerSessions{sessions: spawns}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(spawns.cfgs) != 1 || spawns.cfgs[0].ParentSessionID != "orch-1" {
			t.Fatalf("%s: spawn cfgs = %+v, want ParentSessionID orch-1", name, spawns.cfgs)
		}
	}
}
