package claudecode

import (
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestLaunchSettingsTurnAPercentIntoTokens(t *testing.T) {
	// A percent is of a fixed 1M: 70% is 700k on a 1M model.
	got := claudeLaunchSettingsArgs(domain.AgentLaunchOptions{AutoCompactWindow: "70%", ModelContextWindow: 1_000_000}, "", nil, agentpolicy.Decision{})
	if len(got) != 2 || !strings.Contains(got[1], `"autoCompactWindow":700000`) {
		t.Fatalf("settings %#v: want autoCompactWindow 700000", got)
	}
	// 70% does not fit a 200k model: the agent keeps its own default.
	if got := claudeLaunchSettingsArgs(domain.AgentLaunchOptions{AutoCompactWindow: "70%", ModelContextWindow: 200_000}, "", nil, agentpolicy.Decision{}); strings.Contains(got[1], "autoCompact") {
		t.Fatalf("settings %#v: a window the model cannot hold must not be set", got)
	}
	if got := claudeLaunchSettingsArgs(domain.AgentLaunchOptions{AutoCompactWindow: "70%"}, "", nil, agentpolicy.Decision{}); strings.Contains(got[1], "autoCompact") {
		t.Fatalf("settings %#v: an unknown window must not set a threshold", got)
	}
}

func TestLaunchSettingsFloorTheWindowAtTheBaseline(t *testing.T) {
	// An explicit 120k with a measured 66k baseline runs as 166k (B735).
	got := claudeLaunchSettingsArgs(domain.AgentLaunchOptions{AutoCompactWindow: "120k", ModelContextWindow: 1_000_000, AutoCompactBaseline: 66_000}, "", nil, agentpolicy.Decision{})
	if len(got) != 2 || !strings.Contains(got[1], `"autoCompactWindow":166000`) {
		t.Fatalf("settings %#v: want autoCompactWindow 166000", got)
	}
}
