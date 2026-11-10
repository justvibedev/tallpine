package lifecycle

import (
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// Kiro's approval dialogs fire no hook; only the screen poller sees them. A
// post-tool-use proves the dialog was answered, so it must leave waiting_input
// and the next approval of the same turn becomes a new transition. Other
// harnesses keep waiting_input against background tool traffic.
func TestToolPrecedence_KiroPostToolUseLeavesWaitingInput(t *testing.T) {
	for _, tc := range []struct {
		harness domain.AgentHarness
		want    domain.ActivityState
	}{
		{domain.HarnessKiro, domain.ActivityActive},
		{domain.HarnessCodex, domain.ActivityWaitingInput},
	} {
		m, st, _ := newManager()
		seedSignaled(st, "mer-1", domain.ActivityWaitingInput)
		rec := st.sessions["mer-1"]
		rec.Harness = tc.harness
		st.sessions["mer-1"] = rec
		mustApply(t, m, "mer-1", sig(domain.ActivityActive, "post-tool-use", "shell", "tool_1"))
		if got := stateOf(st, "mer-1"); got != tc.want {
			t.Fatalf("%s: state after post-tool-use = %q, want %q", tc.harness, got, tc.want)
		}
	}
}
