package domain

import (
	"strings"
	"testing"
)

func TestNormalizeOrchestratorIntentText(t *testing.T) {
	if got, err := NormalizeOrchestratorIntentText("  hold install \n"); err != nil || got != "hold install" {
		t.Fatalf("got %q %v", got, err)
	}
	for _, bad := range []string{"", "   ", strings.Repeat("x", MaxOrchestratorIntentLength+1)} {
		if _, err := NormalizeOrchestratorIntentText(bad); err == nil {
			t.Fatalf("accepted %d bytes", len(bad))
		}
	}
}

func TestHandoffAndCompactionPointAtIntents(t *testing.T) {
	if !strings.Contains(AutoHandoffNote("s1"), "tallpine orchestrator intent ls") {
		t.Fatal("handoff note must fetch intents")
	}
	if !strings.Contains(IdleCompactInstructions, "resumed orchestrator to run `tallpine orchestrator intent ls`") {
		t.Fatal("compaction instructions must keep intents")
	}
}
