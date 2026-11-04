package controllers

// SilentFailuresChatIssue is a Chat session whose agent is not attached.
type SilentFailuresChatIssue struct {
	SessionID string `json:"sessionId"`
}

// SilentFailuresReportIssue is a project whose worker reports are not being delivered.
type SilentFailuresReportIssue struct {
	ProjectID     string `json:"projectId"`
	Pending       int    `json:"pending" description:"Reports still pending past their delivery time."`
	Claimed       int    `json:"claimed" description:"Reports claimed for delivery but never acknowledged."`
	Undeliverable bool   `json:"undeliverable" description:"Parked with no orchestrator, or delivery unconfirmed."`
	Since         string `json:"since,omitempty" format:"date-time"`
}

// SilentFailuresFallbackIssue is a final-result notice pending past its due time.
type SilentFailuresFallbackIssue struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	DueAt     string `json:"dueAt,omitempty" format:"date-time"`
}

// SilentFailuresResponse lists conditions that fail without any visible error.
type SilentFailuresResponse struct {
	Healthy         bool                          `json:"healthy"`
	ChatNotAttached []SilentFailuresChatIssue     `json:"chatNotAttached"`
	ReportBatches   []SilentFailuresReportIssue   `json:"reportBatches"`
	TurnFallbacks   []SilentFailuresFallbackIssue `json:"turnFallbacks"`
}

// PreviewFileURLResponse is the body of GET /api/v1/sessions/{sessionId}/preview/file-url.
type PreviewFileURLResponse struct {
	URL string `json:"url" description:"Capability-bearing URL that serves the file from the workspace files origin. Opening it does not change the session preview."`
}

// ContextCategoryPayload is one row of a context breakdown, as the agent reports it.
type ContextCategoryPayload struct {
	ID     string `json:"id,omitempty"`
	Name   string `json:"name"`
	Tokens int64  `json:"tokens"`
	Kind   string `json:"kind" enum:"used,free,buffer,deferred"`
}

// ContextMessagePartsPayload splits the messages row; parts the agent did not report are absent.
type ContextMessagePartsPayload struct {
	ToolCalls   int64 `json:"toolCalls,omitempty"`
	ToolResults int64 `json:"toolResults,omitempty"`
	Attachments int64 `json:"attachments,omitempty"`
	Assistant   int64 `json:"assistant,omitempty"`
	User        int64 `json:"user,omitempty"`
}

// ContextMemoryFilePayload is one instruction or memory file in the window.
type ContextMemoryFilePayload struct {
	Path   string `json:"path"`
	Type   string `json:"type,omitempty"`
	Tokens int64  `json:"tokens"`
}

// ContextBreakdownResponse is what fills a running agent's context window. When
// available is false there is no breakdown to show (the agent cannot report one,
// or the chat is not running) and every other field is absent: the client shows
// the totals it already has. Nothing in it is estimated by Tallpine.
type ContextBreakdownResponse struct {
	Available            bool                        `json:"available"`
	Model                string                      `json:"model,omitempty"`
	TotalTokens          int64                       `json:"totalTokens,omitempty"`
	MaxTokens            int64                       `json:"maxTokens,omitempty"`
	AutoCompactThreshold int64                       `json:"autoCompactThreshold,omitempty"`
	Categories           []ContextCategoryPayload    `json:"categories,omitempty"`
	Messages             *ContextMessagePartsPayload `json:"messages,omitempty"`
	MemoryFiles          []ContextMemoryFilePayload  `json:"memoryFiles,omitempty"`
}

// ModelUsageResponse is a session's cumulative token spend by model. Input
// excludes the cache counters; the four counters never overlap.
type ModelUsageResponse struct {
	Models []ModelUsagePayload `json:"models"`
}
