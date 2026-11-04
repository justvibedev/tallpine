package claudecode

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// Claude's workspace trust dialog blocks a reviewer pane unless trust is recorded
// in the config dir the pane launches with, so PreLaunch must see the launch env.
func TestPreLaunchPassesLaunchEnvSoTrustLandsInItsConfigDir(t *testing.T) {
	agent := &captureAgent{}
	r := &Reviewer{agent: agent}
	if err := r.PreLaunch(context.Background(), ports.ReviewInvocation{
		ReviewerID:    "review-w1",
		WorkspacePath: "/ws/w1",
		Env:           map[string]string{"CLAUDE_CONFIG_DIR": "/acct/claude"},
	}); err != nil {
		t.Fatalf("PreLaunch: %v", err)
	}
	if len(agent.prelaunch) != 1 || agent.prelaunch[0].Env["CLAUDE_CONFIG_DIR"] != "/acct/claude" {
		t.Fatalf("prelaunch = %#v, want the reviewer's launch env", agent.prelaunch)
	}
}
