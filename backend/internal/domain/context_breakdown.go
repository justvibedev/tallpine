package domain

// ContextCategoryKind classifies one row of a context breakdown the way the
// agent itself does, never by its display name.
type ContextCategoryKind string

const (
	// ContextKindUsed content occupies the window.
	ContextKindUsed ContextCategoryKind = "used"
	// ContextKindFree is the window that remains.
	ContextKindFree ContextCategoryKind = "free"
	// ContextKindBuffer is the reserve kept for compaction.
	ContextKindBuffer ContextCategoryKind = "buffer"
	// ContextKindDeferred rows are tool schemas that are not in the window.
	ContextKindDeferred ContextCategoryKind = "deferred"
)

// ContextCategory is one row of the breakdown with the agent's own token count.
type ContextCategory struct {
	// ID is the agent's stable row id ("system_prompt", "messages", ...), empty
	// when the agent does not report one.
	ID     string              `json:"id,omitempty"`
	Name   string              `json:"name"`
	Tokens int64               `json:"tokens"`
	Kind   ContextCategoryKind `json:"kind"`
}

// ContextMessageParts splits the messages row. Every field is a count the agent
// reported; a part the agent did not report stays zero and is not shown.
type ContextMessageParts struct {
	ToolCalls   int64 `json:"toolCalls,omitempty"`
	ToolResults int64 `json:"toolResults,omitempty"`
	Attachments int64 `json:"attachments,omitempty"`
	Assistant   int64 `json:"assistant,omitempty"`
	User        int64 `json:"user,omitempty"`
}

// ContextMemoryFile is one instruction or memory file in the window.
type ContextMemoryFile struct {
	Path   string `json:"path"`
	Type   string `json:"type,omitempty"`
	Tokens int64  `json:"tokens"`
}

// ContextBreakdown is what fills a running agent's context window, exactly as the
// agent reports it (Claude Code's /context). Nothing here is estimated by Tallpine: a
// category the agent does not report is absent, and an agent that cannot report a
// breakdown yields none (callers fall back to the totals).
type ContextBreakdown struct {
	Model       string `json:"model,omitempty"`
	TotalTokens int64  `json:"totalTokens"`
	// MaxTokens is the window the percentages are of.
	MaxTokens int64 `json:"maxTokens"`
	// AutoCompactThreshold is where the agent compacts on its own, 0 when it
	// reports none or auto-compaction is off.
	AutoCompactThreshold int64                `json:"autoCompactThreshold,omitempty"`
	Categories           []ContextCategory    `json:"categories"`
	Messages             *ContextMessageParts `json:"messages,omitempty"`
	MemoryFiles          []ContextMemoryFile  `json:"memoryFiles,omitempty"`
}
