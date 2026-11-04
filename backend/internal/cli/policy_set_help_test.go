package cli

import (
	"strings"
	"testing"
)

// B458: the help told agents they may turn Security mode on install-wide, but
// the daemon admits only an orchestrator turning it on for its own project
// (settingswrite.agentMayChange); install-wide got AGENT_NOT_PERMITTED.
func TestPolicySetHelpMatchesWhatAgentsMayChange(t *testing.T) {
	long := newPolicySetCommand(&commandContext{}).Long
	if !strings.Contains(long, "orchestrator may turn security-mode on for its own project") {
		t.Fatalf("help must say an orchestrator may turn it on for its own project only:\n%s", long)
	}
	if strings.Contains(long, "an agent token may turn security-mode on;") {
		t.Fatalf("help must not promise agents an install-wide switch:\n%s", long)
	}
}
