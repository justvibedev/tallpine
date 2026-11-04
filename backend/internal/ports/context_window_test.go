package ports

import "testing"

func TestContextWindowForEmptyModel(t *testing.T) {
	marked := AgentModelCatalog{Models: []AgentModelInfo{{ID: "big", ContextWindow: 1_000_000}, {ID: "def", ContextWindow: 200_000, IsDefault: true}}}
	if got := marked.ContextWindowFor(""); got != 200_000 {
		t.Fatalf("default marked: %d, want 200000", got)
	}
	unmarked := AgentModelCatalog{Models: []AgentModelInfo{{ID: "a", ContextWindow: 200_000}, {ID: "b", ContextWindow: 1_000_000}}}
	if got := unmarked.ContextWindowFor(""); got != 1_000_000 {
		t.Fatalf("no default: %d, want the largest", got)
	}
	if got := unmarked.ContextWindowFor("missing"); got != 0 {
		t.Fatalf("unlisted named model: %d, want 0", got)
	}
}
