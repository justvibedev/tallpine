package sessionmanager

import (
	"strings"
	"testing"
)

func TestOrchestratorReviewWorkflowChecksPRAgainstBrief(t *testing.T) {
	for _, tracked := range []bool{true, false} {
		got := orchestratorReviewWorkflow(tracked)
		if !strings.Contains(got, "compare its file list and summary with the backlog item's note and done-when (or the brief) before you start a review") {
			t.Fatalf("tracked=%v: review workflow lacks the check-PR-against-brief rule:\n%s", tracked, got)
		}
	}
}
