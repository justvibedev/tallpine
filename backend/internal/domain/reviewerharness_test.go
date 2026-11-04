package domain

import "testing"

func TestSupportedReviewerHarnessSet(t *testing.T) {
	want := []ReviewerHarness{ReviewerClaudeCode, ReviewerCodex, ReviewerKiro, ReviewerDeepSeek}
	if len(AllReviewerHarnesses) != len(want) {
		t.Fatalf("AllReviewerHarnesses = %v, want %v", AllReviewerHarnesses, want)
	}
	for i, h := range want {
		if AllReviewerHarnesses[i] != h || !h.IsKnown() {
			t.Fatalf("AllReviewerHarnesses[%d] = %q, want %q", i, AllReviewerHarnesses[i], h)
		}
	}
}

func TestRemovedReviewerHarnessFallsBackToClaudeCode(t *testing.T) {
	for _, h := range []ReviewerHarness{"opencode", "opencode-v2", "pi", "aider"} {
		if !h.IsUnsupported() {
			t.Fatalf("%q.IsUnsupported() = false, want true", h)
		}
		if got, replaced := h.OrFallback(); got != ReviewerClaudeCode || !replaced {
			t.Fatalf("%q.OrFallback() = (%q, %v), want (claude-code, true)", h, got, replaced)
		}
	}
	for _, h := range []ReviewerHarness{"", ReviewerClaudeCode, ReviewerCodex, ReviewerKiro, ReviewerDeepSeek} {
		if got, replaced := h.OrFallback(); got != h || replaced {
			t.Fatalf("%q.OrFallback() = (%q, %v), want unchanged", h, got, replaced)
		}
	}
}
