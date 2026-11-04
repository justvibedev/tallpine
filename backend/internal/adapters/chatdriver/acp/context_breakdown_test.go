package acp

import (
	"context"
	"encoding/json"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

const claudeContextUsage = `{
  "model": "claude-opus-5-5",
  "totalTokens": 50000, "maxTokens": 200000, "rawMaxTokens": 1000000, "percentage": 25,
  "autoCompactThreshold": 140000, "isAutoCompactEnabled": true,
  "categories": [
    {"id": "system_prompt", "name": "System prompt", "tokens": 6000, "kind": "used"},
    {"id": "messages", "name": "Messages", "tokens": 30000, "kind": "used"},
    {"id": "mcp_tools_deferred", "name": "MCP tools (deferred)", "tokens": 900, "kind": "deferred", "isDeferred": true},
    {"id": "free_space", "name": "Free space", "tokens": 110000, "kind": "free"},
    {"id": "autocompact_buffer", "name": "Autocompact buffer", "tokens": 40000, "kind": "buffer"}
  ],
  "memoryFiles": [{"path": "/repo/CLAUDE.md", "type": "Project", "tokens": 1200}, {"path": "/home/u/.claude/CLAUDE.md", "type": "User", "tokens": 0}],
  "messageBreakdown": {"toolCallTokens": 4000, "toolResultTokens": 20000, "attachmentTokens": 0, "assistantMessageTokens": 3000, "userMessageTokens": 3000}
}`

func TestContextBreakdownReportsTheAgentsOwnCategories(t *testing.T) {
	conv := openCompactionConversation(t, &fakeAgent{contextUsageReply: json.RawMessage(claudeContextUsage)})
	got, supported, err := conv.ContextBreakdown(context.Background())
	if err != nil || !supported {
		t.Fatalf("supported = %v, err = %v", supported, err)
	}
	if got.Model != "claude-opus-5-5" || got.TotalTokens != 50000 || got.MaxTokens != 200000 || got.AutoCompactThreshold != 140000 {
		t.Fatalf("totals = %+v", got)
	}
	kinds := map[string]domain.ContextCategoryKind{}
	for _, category := range got.Categories {
		kinds[category.ID] = category.Kind
	}
	want := map[string]domain.ContextCategoryKind{
		"system_prompt": domain.ContextKindUsed, "messages": domain.ContextKindUsed, "mcp_tools_deferred": domain.ContextKindDeferred,
		"free_space": domain.ContextKindFree, "autocompact_buffer": domain.ContextKindBuffer,
	}
	for id, kind := range want {
		if kinds[id] != kind {
			t.Errorf("category %s kind = %q, want %q", id, kinds[id], kind)
		}
	}
	if got.Messages == nil || got.Messages.ToolResults != 20000 || got.Messages.ToolCalls != 4000 {
		t.Fatalf("message parts = %+v", got.Messages)
	}
	// A memory file that costs nothing is not a row.
	if len(got.MemoryFiles) != 1 || got.MemoryFiles[0].Path != "/repo/CLAUDE.md" {
		t.Fatalf("memory files = %+v", got.MemoryFiles)
	}
}

// Nothing is invented: an unpatched bridge or an empty reply is "no breakdown".
func TestContextBreakdownIsAbsentRatherThanGuessed(t *testing.T) {
	if _, supported, err := openCompactionConversation(t, &fakeAgent{}).ContextBreakdown(context.Background()); err != nil || supported {
		t.Fatalf("unpatched bridge: supported = %v, err = %v; want false, nil", supported, err)
	}
	empty := openCompactionConversation(t, &fakeAgent{contextUsageReply: json.RawMessage(`{"categories": [], "maxTokens": 0}`)})
	if _, supported, err := empty.ContextBreakdown(context.Background()); err != nil || supported {
		t.Fatalf("empty reply: supported = %v, err = %v; want false, nil", supported, err)
	}
}

func TestBreakdownFromReplyHandlesOlderAndOddReplies(t *testing.T) {
	var reply contextUsageReply
	raw := `{"totalTokens": 10, "maxTokens": 100, "autoCompactThreshold": 70, "isAutoCompactEnabled": false,
	  "categories": [{"name": "Old row", "tokens": 5}, {"name": "Deferred", "tokens": 3, "isDeferred": true}, {"name": "Bad", "tokens": -1, "kind": "used"}]}`
	if err := json.Unmarshal([]byte(raw), &reply); err != nil {
		t.Fatal(err)
	}
	got, supported, _ := breakdownFromReply(reply)
	if !supported || got.AutoCompactThreshold != 0 {
		t.Fatalf("supported = %v threshold = %d; auto-compaction is off so no threshold", supported, got.AutoCompactThreshold)
	}
	if len(got.Categories) != 2 || got.Categories[0].Kind != domain.ContextKindUsed || got.Categories[1].Kind != domain.ContextKindDeferred {
		t.Fatalf("categories = %+v; want the kindless row used, the deferred flag honoured, the negative row dropped", got.Categories)
	}
}
