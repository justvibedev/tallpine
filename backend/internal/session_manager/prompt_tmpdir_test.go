package sessionmanager

import (
	"strings"
	"testing"
)

// B567: an approving orchestrator must know the worker's own $TMPDIR is
// session-owned, while the rule against outside-workspace deletes stays.
func TestOrchestratorPromptTreatsWorkerTmpdirAsSessionOwned(t *testing.T) {
	got := buildSystemPromptText(systemPromptConfig{Role: sessionPromptRoleOrchestrator, Project: promptProject{ID: "tp", Name: "Tallpine"}})
	for _, want := range []string{"`$TMPDIR`", "~/.tallpine/data/tmp/<session>", "session-owned", "deletes outside the workspace"} {
		if !strings.Contains(got, want) {
			t.Errorf("orchestrator prompt lacks %q", want)
		}
	}
}
