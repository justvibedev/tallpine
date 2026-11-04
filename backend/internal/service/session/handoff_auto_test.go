package session

import (
	"context"
	"os"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/sessionartifacts"
)

// The idle handoff writes the fixed bootstrap note itself and then runs the
// normal handoff: the old orchestrator retires, a successor spawns.
func TestAutoHandoffOrchestratorWritesTheTemplateAndHandsOff(t *testing.T) {
	f := newHandoffFixture(t, domain.HarnessClaudeCode)
	if err := f.svc.AutoHandoffOrchestrator(context.Background(), "mer-1"); err != nil {
		t.Fatalf("AutoHandoffOrchestrator: %v", err)
	}
	if len(f.fc.retired) != 1 || f.fc.retired[0] != "mer-1" {
		t.Fatalf("retired = %v, want [mer-1]", f.fc.retired)
	}
	entries, err := os.ReadDir(sessionartifacts.Dir(f.svc.dataDir, "mer-1"))
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "auto-handoff-") {
			continue
		}
		found = true
		b, _ := os.ReadFile(sessionartifacts.Dir(f.svc.dataDir, "mer-1") + "/" + e.Name())
		if !strings.Contains(string(b), "tallpine merges") || !strings.Contains(string(b), "mer-1") {
			t.Fatalf("note lacks live-state commands or the previous id:\n%s", b)
		}
	}
	if !found {
		t.Fatal("no auto-handoff note was written")
	}
}

// A refused handoff (no orchestrator agent configured) leaves the old
// orchestrator running.
func TestAutoHandoffOrchestratorRefusalKeepsTheOldOne(t *testing.T) {
	f := newHandoffFixture(t, "")
	if err := f.svc.AutoHandoffOrchestrator(context.Background(), "mer-1"); err == nil {
		t.Fatal("handoff without a configured orchestrator agent succeeded")
	}
	if len(f.fc.retired) != 0 {
		t.Fatalf("retired = %v, want none", f.fc.retired)
	}
}
