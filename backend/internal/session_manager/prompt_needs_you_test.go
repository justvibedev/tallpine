package sessionmanager

import (
	"strings"
	"testing"
)

// The frontend (WorkerUpdates.tsx hasNeedsYouMarker) parses this exact marker.
func TestOrchestratorPromptCarriesTheNeedsYouMarker(t *testing.T) {
	project := promptProject{ID: "tp", Name: "Tallpine"}
	const marker = "`**Needs you:**`"
	if got := buildSystemPromptText(systemPromptConfig{Role: sessionPromptRoleOrchestrator, Project: project}); !strings.Contains(got, marker) {
		t.Fatalf("orchestrator prompt lacks %s", marker)
	}
	for name, cfg := range map[string]systemPromptConfig{
		"worker":            {Role: sessionPromptRoleWorker, Project: project, OrchestratorSessionID: "tp-orch"},
		"standalone worker": {Role: sessionPromptRoleWorker, Standalone: true},
	} {
		if got := buildSystemPromptText(cfg); strings.Contains(got, "Needs you:") {
			t.Errorf("%s prompt carries the marker rule", name)
		}
	}
}
