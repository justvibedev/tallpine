package sessionmanager

import (
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// A code-intel spawn's system prompt carries the LSP instruction; a plain one,
// and the orchestrator's own prompt, do not. The orchestrator prompt names the flag.
func TestCodeIntelSystemPrompt(t *testing.T) {
	t.Parallel()
	m, _, _, _ := newManager()
	cfg := ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode}
	_, plain, err := m.buildSpawnTexts(ctx, cfg, "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	cfg.CodeIntel = true
	_, on, err := m.buildSpawnTexts(ctx, cfg, "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain, "Shell grep/rg are disabled") || !strings.Contains(on, domain.CodeIntelPrompt) {
		t.Fatalf("plain has instruction=%v, code-intel has it=%v", strings.Contains(plain, "Shell grep/rg"), strings.Contains(on, domain.CodeIntelPrompt))
	}
	if !strings.Contains(orchestratorSystemPrompt(promptProject{ID: "p", Name: "P"}), "--code-intel") {
		t.Fatal("orchestrator prompt does not mention --code-intel")
	}
}
