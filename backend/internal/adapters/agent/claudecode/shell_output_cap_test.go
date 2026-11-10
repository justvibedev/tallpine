package claudecode

import (
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestLaunchSettingsCapShellOutput(t *testing.T) {
	got := claudeLaunchSettingsArgs(domain.AgentLaunchOptions{ShellOutputChars: 12000}, "", nil, agentpolicy.Decision{})
	if len(got) != 2 || !strings.Contains(got[1], `"bashOutputMaxChars":12000`) {
		t.Fatalf("settings %#v: want bashOutputMaxChars 12000", got)
	}
	got = claudeLaunchSettingsArgs(domain.AgentLaunchOptions{}, "", nil, agentpolicy.Decision{})
	if len(got) == 2 && strings.Contains(got[1], "bashOutputMaxChars") {
		t.Fatalf("settings %#v: cap set without a limit", got)
	}
}
