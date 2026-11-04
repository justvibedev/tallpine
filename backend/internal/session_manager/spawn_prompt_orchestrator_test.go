package sessionmanager

import (
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// The orchestrator handoff seeds the new orchestrator through
// SpawnConfig.Prompt; the real prompt builder must pass it through verbatim.
func TestBuildPromptKeepsTheOrchestratorSeedMessage(t *testing.T) {
	const seed = "Read /d/artifacts/o-1/state.md and continue. You replaced orchestrator o-1; worker reports now come to you."
	if got := buildPrompt(ports.SpawnConfig{Kind: domain.KindOrchestrator, Prompt: seed}); got != seed {
		t.Fatalf("buildPrompt = %q, want the seed message unchanged", got)
	}
}
