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

func TestNewestClaudeFamilyModel(t *testing.T) {
	ids := []string{"claude-haiku-4-5", "claude-haiku-5-5", "claude-sonnet-5-5", "claude-opus-5-5", "claude-opus-4-1", "claude-fable-5-2", "claude-sonnet-5-5-20260101"}
	cands := make([]ClaudeFamilyCandidate, len(ids))
	for i, id := range ids {
		cands[i] = ClaudeFamilyCandidate{ID: id}
	}
	for alias, want := range map[string]string{
		"haiku": "claude-haiku-5-5", "Sonnet": "claude-sonnet-5-5", "opus": "claude-opus-5-5", "opus[1m]": "claude-opus-5-5", "fable": "claude-fable-5-2",
	} {
		if i := NewestClaudeFamilyModel(cands, alias); i < 0 || ids[i] != want {
			t.Errorf("%s -> %d; want %q", alias, i, want)
		}
	}
	for _, m := range []string{"claude-haiku-5-5", "gpt-5", "mythos", ""} {
		if i := NewestClaudeFamilyModel(cands, m); i >= 0 {
			t.Errorf("%q resolved to %q; want no alias", m, ids[i])
		}
	}
	if i := NewestClaudeFamilyModel(cands[3:4], "haiku"); i >= 0 {
		t.Error("alias with no family model resolved")
	}
	cands[1].Skip = true
	if i := NewestClaudeFamilyModel(cands, "haiku"); i < 0 || ids[i] != "claude-haiku-4-5" {
		t.Errorf("skip not honored: %d", i)
	}
}
