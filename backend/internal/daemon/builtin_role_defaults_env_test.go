package daemon

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestAgentNotReadyHonoursHermeticSwitch(t *testing.T) {
	b := &boot{}
	if b.agentNotReady(context.Background(), domain.HarnessClaudeCode) {
		t.Fatal("agent reported not ready without the switch")
	}
	t.Setenv(noBuiltinRoleDefaultsEnv, "1")
	if !b.agentNotReady(context.Background(), domain.HarnessClaudeCode) {
		t.Fatal("switch set: built-in role defaults must be skipped")
	}
}
