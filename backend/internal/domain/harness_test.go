package domain

import (
	"strings"
	"testing"
)

func TestSupportedHarnessSet(t *testing.T) {
	want := []AgentHarness{HarnessClaudeCode, HarnessCodex, HarnessKiro, HarnessDeepSeek}
	if len(AllHarnesses) != len(want) {
		t.Fatalf("AllHarnesses = %v, want %v", AllHarnesses, want)
	}
	for i, h := range want {
		if AllHarnesses[i] != h || !h.IsKnown() {
			t.Fatalf("AllHarnesses[%d] = %q (known=%v), want %q", i, AllHarnesses[i], AllHarnesses[i].IsKnown(), h)
		}
	}
}

func TestRemovedHarnessIsUnsupportedAndFallsBack(t *testing.T) {
	for _, name := range []string{"aider", "opencode", "opencode-v2", "cursor", "hermes", "unreal-agent", "something-new"} {
		h := AgentHarness(name)
		if h.IsKnown() {
			t.Fatalf("%q.IsKnown() = true, want false", name)
		}
		if !h.IsUnsupported() {
			t.Fatalf("%q.IsUnsupported() = false, want true", name)
		}
		if got, replaced := h.OrFallback(); got != HarnessClaudeCode || !replaced {
			t.Fatalf("%q.OrFallback() = (%q, %v), want (claude-code, true)", name, got, replaced)
		}
	}
}

func TestSupportedAndEmptyHarnessesAreNotReplaced(t *testing.T) {
	for _, h := range []AgentHarness{"", HarnessClaudeCode, HarnessCodex, HarnessKiro, HarnessFake} {
		if h.IsUnsupported() {
			t.Fatalf("%q.IsUnsupported() = true, want false", h)
		}
		if got, replaced := h.OrFallback(); got != h || replaced {
			t.Fatalf("%q.OrFallback() = (%q, %v), want unchanged", h, got, replaced)
		}
	}
}

func TestUnsupportedHarnessMessageNamesHarnessAndOptions(t *testing.T) {
	msg := UnsupportedHarnessMessage("aider")
	for _, want := range []string{`"aider"`, "read-only", "claude-code, codex or kiro"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message %q does not mention %q", msg, want)
		}
	}
}

func TestRemovedHarnessNamesExcludeSupportedAndTypos(t *testing.T) {
	for _, h := range AllHarnesses {
		if h.IsRemoved() {
			t.Fatalf("supported harness %q is listed as removed", h)
		}
	}
	for _, h := range AllReviewerHarnesses {
		if h.IsRemoved() {
			t.Fatalf("supported reviewer %q is listed as removed", h)
		}
	}
	if !AgentHarness("opencode").IsRemoved() || !ReviewerHarness("aider").IsRemoved() {
		t.Fatal("previously shipped harness not recognised as removed")
	}
	if AgentHarness("claud-code").IsRemoved() || ReviewerHarness("nope").IsRemoved() {
		t.Fatal("a typo was treated as a removed harness")
	}
}
