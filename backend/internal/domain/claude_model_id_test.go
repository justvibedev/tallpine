package domain

import "testing"

func TestIsClaudeModelID(t *testing.T) {
	for id, want := range map[string]bool{
		"claude-fable-5-2": true, "claude-opus-5-5[1m]": true, "claude-opus-4-5-20251101": true, "claude-haiku-5": true,
		"claude-fabel-5-2": false, "fable": false, "claude-fable-5-": false, "claude-fable-x": false, "gpt-5": false, "": false,
	} {
		if got := IsClaudeModelID(id); got != want {
			t.Errorf("IsClaudeModelID(%q) = %v, want %v", id, got, want)
		}
	}
}

func TestAcceptsUnlistedClaudeModelIDNeedsAFirstPartyCatalog(t *testing.T) {
	first := []string{"claude-fable-5-1", "claude-opus-5-5"}
	bedrock := []string{"anthropic.claude-opus-5-5-v1:0", "us.anthropic.claude-fable-5-1-v1:0"}
	vertex := []string{"claude-opus-5-5@20260901"}
	if !AcceptsUnlistedClaudeModelID("claude-fable-5-2", first) {
		t.Error("first-party catalog must accept a well-formed unlisted ID")
	}
	for name, listed := range map[string][]string{"bedrock": bedrock, "vertex": vertex, "empty": nil} {
		if AcceptsUnlistedClaudeModelID("claude-fable-5-2", listed) {
			t.Errorf("%s catalog accepted a first-party-shaped unlisted ID", name)
		}
	}
	if AcceptsUnlistedClaudeModelID("claude-fabel-5-2", first) {
		t.Error("malformed ID accepted")
	}
}
