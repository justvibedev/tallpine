package activitydispatch

import (
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// Every deriver key must be a known harness name except fake, whose deriver is
// retained for test fixtures and historical callbacks even though the harness is
// no longer user-selectable. SupportsHarness equates tokens and harnesses, so any
// other drift would silently report a hooked harness as hook-less.
func TestDeriverTokensAreKnownHarnesses(t *testing.T) {
	for token := range Derivers {
		if token == string(domain.HarnessFake) {
			continue
		}
		if !domain.AgentHarness(token).IsKnown() {
			t.Errorf("deriver token %q is not a known AgentHarness", token)
		}
	}
}

func TestSupportsHarness(t *testing.T) {
	for _, h := range []domain.AgentHarness{domain.HarnessCodex, domain.HarnessClaudeCode, domain.HarnessKiro, domain.HarnessDeepSeek} {
		if !SupportsHarness(h) {
			t.Errorf("SupportsHarness(%q) = false, want true", h)
		}
	}
	// Harnesses with no callback pipeline, including removed ones, must read as
	// unsupported.
	for _, h := range []domain.AgentHarness{domain.AgentHarness("aider"), domain.AgentHarness("opencode"), domain.AgentHarness("")} {
		if SupportsHarness(h) {
			t.Errorf("SupportsHarness(%q) = true, want false", h)
		}
	}
}

func TestRemovedAgentTokensDeriveNothing(t *testing.T) {
	for _, token := range []string{"aider", "opencode", "cursor", "grok", "muse"} {
		if got, ok := Derive(token, "stop", []byte(`{}`)); ok {
			t.Errorf("Derive(%q, stop) = (%q, true), want no activity", token, got)
		}
	}
}

func TestSignalCoverageForHarness(t *testing.T) {
	tests := []struct {
		harness domain.AgentHarness
		want    SignalCoverage
	}{
		{domain.HarnessClaudeCode, SignalCoverageComplete},
		{domain.HarnessCodex, SignalCoverageComplete},
		{domain.HarnessKiro, SignalCoverageComplete},
		{domain.HarnessDeepSeek, SignalCoverageComplete},
		{domain.AgentHarness("aider"), SignalCoverageNone},
	}

	for _, tt := range tests {
		t.Run(string(tt.harness), func(t *testing.T) {
			if got := CoverageForHarness(tt.harness); got != tt.want {
				t.Fatalf("CoverageForHarness(%q) = %v, want %v", tt.harness, got, tt.want)
			}
		})
	}
}

func TestFullySupportsHarnessRequiresCompleteCoverage(t *testing.T) {
	if !FullySupportsHarness(domain.HarnessClaudeCode) {
		t.Fatal("FullySupportsHarness(claude-code) = false, want true")
	}
	if FullySupportsHarness(domain.AgentHarness("aider")) {
		t.Fatal("FullySupportsHarness(aider) = true, want false for a removed harness")
	}
}
