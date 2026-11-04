package acp

import (
	"encoding/json"
	"math"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/commanddetail"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func (c *conversation) mergeToolUpdate(update *acpsdk.SessionToolCallUpdate) *toolState {
	id := string(update.ToolCallId)
	c.mu.Lock()
	defer c.mu.Unlock()
	tool := c.tools[id]
	if tool == nil {
		tool = &toolState{id: id}
		c.tools[id] = tool
	}
	if update.Title != nil {
		tool.title = *update.Title
	}
	if update.Kind != nil {
		tool.kind = *update.Kind
	}
	if update.Status != nil {
		tool.status = *update.Status
	}
	if update.Locations != nil {
		tool.locations = update.Locations
	}
	if update.Content != nil {
		tool.content = update.Content
	}
	if update.RawInput != nil {
		tool.rawInput = update.RawInput
	}
	if update.RawOutput != nil {
		tool.rawOutput = update.RawOutput
	}
	tool.meta = mergeMeta(tool.meta, update.Meta)
	if delta := terminalOutput(update.Meta); delta != "" {
		tool.terminalOutput += delta
	}
	snapshot := *tool
	return &snapshot
}

func (c *conversation) toolEvent(turnID string, tool *toolState, completed bool) ports.ChatEvent {
	output := toolOutputText(withoutImageData(tool.rawOutput))
	if tool.terminalOutput != "" {
		output = tool.terminalOutput
	}
	activityKind := activityKindFromTool(tool.kind)
	// The detail is rewritten on every tool update and carried by every snapshot,
	// so a page an agent hands html_render and the screenshot html_preview hands
	// back are not stored whole, and neither is output or a diff that a provider
	// re-sends in full on every update.
	output, outputCut := commanddetail.TailChars(output, commanddetail.MaxOutputChars)
	var content any = contentWithoutImageData(tool.content)
	if activityKind != domain.ActivityKindMCPTool {
		// Only an MCP-style row renders its content, and for a read or a fetch that
		// is all the timeline shows of the result. Other rows render other fields (a
		// command its output, a file change its files), so their content is held to
		// the tool payload cap.
		content = cappedPayload(content)
	}
	detailMap := map[string]any{
		"protocol": "acp", "toolKind": tool.kind, "locations": tool.locations,
		"input": cappedPayload(tool.rawInput), "output": output, "content": content,
	}
	if outputCut {
		detailMap["outputTruncated"] = true
	}
	if activityKind == domain.ActivityKindFileChange {
		files := make([]map[string]any, 0)
		for _, item := range tool.content {
			if item.Diff == nil {
				continue
			}
			stats := diffFileFromSnapshot(item.Diff.Path, item.Diff.OldText, item.Diff.NewText)
			patch := acpFilePatch(item.Diff.Path, item.Diff.OldText, item.Diff.NewText)
			file := map[string]any{
				"path": item.Diff.Path, "status": stats.Status,
				"additions": stats.Additions, "deletions": stats.Deletions,
				"patch": patch, "oldText": item.Diff.OldText, "newText": item.Diff.NewText,
			}
			if len(patch) > commanddetail.MaxPatchChars {
				// A client reads the full texts only for a file with no patch, so
				// past the cap they are not stored.
				capped, cut := cappedACPPatch(item.Diff.Path, item.Diff.OldText, item.Diff.NewText)
				file["patch"] = capped
				if cut {
					file["patchTruncated"] = true
				}
				delete(file, "oldText")
				delete(file, "newText")
			}
			files = append(files, file)
		}
		if len(files) > 0 {
			detailMap["files"] = files
		}
	}
	if claude := nestedMap(tool.meta, "claudeCode"); claude != nil {
		copyDetail(detailMap, claude, "toolName", "providerToolName")
		if parentID, ok := claude["parentToolUseId"].(string); ok && parentID != "" {
			detailMap["parentProviderItemId"] = c.providerItemID(parentID)
		}
		copyDetail(detailMap, claude, "subagent", "nestedAgent")
		copyDetail(detailMap, claude, "subagentType", "subagentType")
		copyDetail(detailMap, claude, "subagentRetry", "subagentRetry")
		if title, ok := claude["title"].(string); ok && strings.TrimSpace(title) != "" {
			detailMap["providerTitle"] = title
		}
	}
	// claude-agent-acp 0.99 gives an AIR client (Tallpine declares jetbrains.air) the
	// subagent marker and the shell description under that namespace instead of
	// _meta.claudeCode (subagent, title); read both so either release renders.
	if air := nestedMap(nestedMap(tool.meta, "jetbrains"), "air"); air != nil {
		if subagent, _ := air["subagent"].(bool); subagent {
			detailMap["nestedAgent"] = true
		}
		if title, ok := air["commandTitle"].(string); ok && strings.TrimSpace(title) != "" {
			if _, exists := detailMap["providerTitle"]; !exists {
				detailMap["providerTitle"] = title
			}
		}
	}
	if terminal := nestedMap(tool.meta, "terminal_info"); terminal != nil {
		copyDetail(detailMap, terminal, "terminal_id", "terminalId")
	}
	if terminal := nestedMap(tool.meta, "terminal_exit"); terminal != nil {
		copyDetail(detailMap, terminal, "exit_code", "exitCode")
		copyDetail(detailMap, terminal, "signal", "signal")
	}
	if activityKind == domain.ActivityKindCommand {
		// OpenCode includes the shell result in rawOutput.metadata.exit, even
		// when ACP marks the tool call completed. Preserve it separately from
		// output text; an explicit terminal_exit remains authoritative.
		if _, exists := detailMap["exitCode"]; !exists {
			if raw, ok := tool.rawOutput.(map[string]any); ok {
				if exit, ok := number(nestedMap(raw, "metadata")["exit"]); ok &&
					!math.IsNaN(exit) && !math.IsInf(exit, 0) && math.Trunc(exit) == exit {
					detailMap["exitCode"] = exit
				}
			}
		}
		if rawCommand := rawCommandFromInput(tool.rawInput); rawCommand != "" {
			// The neutral command-detail contract (`detail.command`) is what the
			// chat timeline renders as the row's subject. rawInput is a
			// provider-shaped object (claude-code Bash: {"command": "..."}); the
			// codex driver sets this key directly, and ACP-backed harnesses must
			// too or the UI can only ever say "Ran command".
			detailMap["command"] = commanddetail.UnwrapShell(rawCommand)
			// Keep the exact provider value beside the display form. The timeline is
			// an audit surface, so callers must be able to recover what actually ran
			// even when a provider wraps it in a shell invocation.
			detailMap["rawCommand"] = rawCommand
		}
	}
	detail, _ := json.Marshal(detailMap)
	status := activityStatusFromTool(tool.status)
	if completed && activityKind == domain.ActivityKindCommand && status == domain.ActivityStatusCompleted {
		if exit, ok := number(detailMap["exitCode"]); ok && exit != 0 {
			status = domain.ActivityStatusFailed
		}
	}
	kind := ports.ChatEventActivityStarted
	if completed {
		kind = ports.ChatEventActivityCompleted
	}
	summary := strings.TrimSpace(tool.title)
	if summary == "" {
		summary = "Agent tool"
	}
	return ports.ChatEvent{
		Kind: kind, ProviderTurnID: turnID, ProviderItemID: c.providerItemID(tool.id),
		ActivityKind: activityKind, ActivityStatus: status,
		Summary: summary, Detail: detail,
	}
}

func activityKindFromTool(kind acpsdk.ToolKind) domain.ActivityKind {
	switch kind {
	case acpsdk.ToolKindExecute:
		return domain.ActivityKindCommand
	case acpsdk.ToolKindEdit, acpsdk.ToolKindDelete, acpsdk.ToolKindMove:
		return domain.ActivityKindFileChange
	case acpsdk.ToolKindThink:
		return domain.ActivityKindReasoning
	default:
		return domain.ActivityKindMCPTool
	}
}

func activityStatusFromTool(status acpsdk.ToolCallStatus) domain.ActivityStatus {
	switch status {
	case acpsdk.ToolCallStatusCompleted:
		return domain.ActivityStatusCompleted
	case acpsdk.ToolCallStatusFailed:
		return domain.ActivityStatusFailed
	case acpsdk.ToolCallStatusPending:
		return domain.ActivityStatusPending
	default:
		return domain.ActivityStatusRunning
	}
}

func toolTerminal(status acpsdk.ToolCallStatus) bool {
	return status == acpsdk.ToolCallStatusCompleted || status == acpsdk.ToolCallStatusFailed
}

func (c *conversation) emitDiffs(
	turnID, toolID string,
	content []acpsdk.ToolCallContent,
	emit func(ports.ChatEvent),
) {
	// One tool call contributes at most one entry per path. If the provider
	// lists the same path more than once in a single content payload, the last
	// snapshot wins — summing those would reintroduce inflated counts.
	byPath := make(map[string]ports.ChatDiffFile)
	var pathOrder []string
	for _, item := range content {
		if item.Diff == nil {
			continue
		}
		path := item.Diff.Path
		if _, exists := byPath[path]; !exists {
			pathOrder = append(pathOrder, path)
		}
		byPath[path] = diffFileFromSnapshot(path, item.Diff.OldText, item.Diff.NewText)
	}
	if len(byPath) == 0 {
		return
	}
	files := make([]ports.ChatDiffFile, 0, len(pathOrder))
	for _, path := range pathOrder {
		files = append(files, byPath[path])
	}
	c.mu.Lock()
	if c.turnDiffTurnID != turnID || c.turnDiffs == nil {
		c.turnDiffs = &turnDiffAccumulator{}
		c.turnDiffTurnID = turnID
	}
	c.turnDiffs.replaceTool(toolID, files)
	aggregated := c.turnDiffs.aggregate()
	c.mu.Unlock()
	emit(ports.ChatEvent{Kind: ports.ChatEventTurnDiff, ProviderTurnID: turnID, Diff: &ports.ChatTurnDiff{Files: aggregated}})
}
