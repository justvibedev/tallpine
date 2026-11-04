package sessionmanager

import (
	"strings"
	"testing"
)

// Both the tracked and the remoteless orchestrator workflows say how a no-PR
// branch is reviewed and landed.
func TestOrchestratorWorkflowNamesSessionLand(t *testing.T) {
	for _, tracked := range []bool{true, false} {
		text := orchestratorReviewWorkflow(tracked)
		if !strings.Contains(text, "tallpine session land <session>") || !strings.Contains(text, "tallpine pr land") {
			t.Fatalf("tracked=%v workflow lacks the land text:\n%s", tracked, text)
		}
	}
}
