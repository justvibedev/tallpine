package daemon

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type spawnSpy struct {
	planSessions
	cfg ports.SpawnConfig
}

func (s *spawnSpy) Spawn(_ context.Context, cfg ports.SpawnConfig) (domain.Session, int, int, error) {
	s.cfg = cfg
	return domain.Session{ID: "a1"}, 0, 0, nil
}

func TestContextAnalystSpawnsClaudeHaikuLowEffort(t *testing.T) {
	spy := &spawnSpy{}
	a := newContextAnalyst(t.TempDir(), spy, nil, nil)
	if _, err := a.Spawn(context.Background(), "p", "task"); err != nil {
		t.Fatal(err)
	}
	c := spy.cfg
	if c.Harness != domain.HarnessClaudeCode || c.AgentConfig.Model != "haiku" || c.AgentConfig.Effort != "low" || c.Role != domain.SessionRoleContextAnalyst || c.Kind != domain.KindWorker || c.ParentSessionID != "" {
		t.Fatalf("spawn config = %+v", c)
	}
}
