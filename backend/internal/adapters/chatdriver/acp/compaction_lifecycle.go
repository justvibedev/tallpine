package acp

import (
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// compactionFromTool reads claude-agent-acp's compaction lifecycle. A client
// that does not advertise the compaction session capability gets it as a
// synthetic "compact" tool call (context-compaction.js, tool-calls/reporters/
// compaction.js): _meta.claudeCode.toolName is "compact", and the terminal
// report carries {trigger, preTokens, postTokens, durationMs} in rawOutput.
func compactionFromTool(tool *toolState) (*ports.ChatCompaction, bool) {
	claude, _ := tool.meta["claudeCode"].(map[string]any)
	if name, _ := claude["toolName"].(string); name != "compact" {
		return nil, false
	}
	report := &ports.ChatCompaction{ID: tool.id, Status: domain.CompactionRunning}
	switch tool.status {
	case "completed":
		report.Status = domain.CompactionCompleted
	case "failed":
		report.Status = domain.CompactionFailed
	}
	if facts, ok := tool.rawOutput.(map[string]any); ok {
		switch facts["trigger"] {
		case "automatic":
			report.Trigger = "auto"
		case "manual":
			report.Trigger = "manual"
		}
		report.PreTokens = factTokens(facts["preTokens"])
		report.PostTokens = factTokens(facts["postTokens"])
	}
	return report, true
}

func factTokens(value any) int64 {
	if n, ok := value.(float64); ok && n > 0 {
		return int64(n)
	}
	return 0
}

// emitCompaction publishes a compaction report and, once it finished with a
// post-compaction size, moves the context position to it at once instead of
// leaving the pre-compaction figure until the next usage update.
func (c *conversation) emitCompaction(tool *toolState, turnID string, emit func(ports.ChatEvent)) {
	report, ok := compactionFromTool(tool)
	if !ok {
		return
	}
	// The adapter heartbeats the open call; each report is a store write, so a
	// repeat of the last one for this call is dropped.
	c.mu.Lock()
	if c.compactionSent == nil {
		c.compactionSent = map[string]ports.ChatCompaction{}
	}
	repeat := c.compactionSent[tool.id] == *report
	c.compactionSent[tool.id] = *report
	c.mu.Unlock()
	if repeat {
		return
	}
	emit(ports.ChatEvent{Kind: ports.ChatEventThreadState, ProviderTurnID: turnID,
		ThreadState: &ports.ChatThreadState{Compaction: report}})
	if report.Status != domain.CompactionCompleted || report.PostTokens <= 0 {
		return
	}
	c.mu.Lock()
	window := c.contextWindow
	c.mu.Unlock()
	if window <= 0 {
		return
	}
	c.trackContext(report.PostTokens, window)
	emit(ports.ChatEvent{Kind: ports.ChatEventUsage, ProviderTurnID: turnID, Usage: &ports.ChatUsage{
		ContextUsed: report.PostTokens, ContextWindow: window, ContextKnown: true,
	}})
}
