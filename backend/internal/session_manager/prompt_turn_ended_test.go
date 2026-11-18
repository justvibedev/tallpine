package sessionmanager

import (
	"strings"
	"testing"
)

// B799: the knivesvr orchestrator skimmed `[auto] turn_ended` notices as
// routine. Its prompt must say they need action and how to handle one.
func TestOrchestratorPromptActsOnTurnEnded(t *testing.T) {
	t.Parallel()
	got := buildSystemPromptText(systemPromptConfig{Role: sessionPromptRoleOrchestrator, Project: promptProject{ID: "mer", Name: "Mercury"}})
	for _, want := range []string{"`[auto] turn_ended`", "never skip one as routine", "if it answers you, act on that answer", "send`"} {
		if !strings.Contains(got, want) {
			t.Fatalf("orchestrator prompt missing %q", want)
		}
	}
	worker := buildSystemPromptText(systemPromptConfig{Role: sessionPromptRoleWorker, Project: promptProject{ID: "mer", Name: "Mercury"}})
	if strings.Contains(worker, "never skip one as routine") {
		t.Fatal("worker prompt carries the orchestrator's turn_ended rule")
	}
}
