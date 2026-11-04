package acp

import (
	"encoding/json"
	"fmt"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/persistenthost"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// processUpdate runs the full ACP -> Tallpine normalization for one update: live
// notifications and replayed updates during the post-load drain.
func (c *conversation) processUpdate(params acpsdk.SessionNotification) error {
	if c.prepareHistoryUpdate(params.Update) {
		return nil
	}
	c.mu.Lock()
	sessionID := c.sessionID
	turnID := c.activeTurn
	c.mu.Unlock()
	if sessionID != "" && string(params.SessionId) != sessionID {
		return fmt.Errorf("ACP update for unexpected session %q", params.SessionId)
	}
	sourceID := stableACPEventID(params.Meta)
	sourceIndex := 0
	emit := func(event ports.ChatEvent) {
		if sourceID != "" {
			event.ProviderEventID = fmt.Sprintf("%s:%d", sourceID, sourceIndex)
			sourceIndex++
		}
		c.emit(event)
	}

	update := params.Update
	providerOutputResumed := update.AgentMessageChunk != nil ||
		update.AgentThoughtChunk != nil ||
		update.ToolCall != nil ||
		update.Plan != nil
	if providerOutputResumed {
		c.completeProviderFailure(turnID, emit)
	}
	switch {
	case update.AgentMessageChunk != nil:
		if event, tagged, ok := c.mcpServerStatusEvent(turnID, update.AgentMessageChunk.Meta, mcpNoticeDescription(contentText(update.AgentMessageChunk.Content))); tagged {
			if ok {
				emit(event)
			}
			break
		}
		c.mu.Lock()
		isCompacting := c.compactingTurnID != "" && c.compactingTurnID == turnID
		c.mu.Unlock()
		if isCompacting {
			if delta := contentText(update.AgentMessageChunk.Content); delta != "" {
				c.mu.Lock()
				c.compactionSummary += delta
				c.mu.Unlock()
			}
			break
		}
		id := c.providerItemID(messageID(update.AgentMessageChunk.MessageId, "assistant", turnID))
		if delta := contentText(update.AgentMessageChunk.Content); delta != "" {
			if parentID := c.providerItemID(parentToolUseID(update.AgentMessageChunk.Meta)); parentID != "" {
				c.mu.Lock()
				item, existed := c.nestedMessages[id]
				item.text += delta
				item.parentID = parentID
				c.nestedMessages[id] = item
				c.mu.Unlock()
				detail, _ := json.Marshal(map[string]any{"parentProviderItemId": parentID, "nestedAgent": true})
				if !existed {
					emit(ports.ChatEvent{Kind: ports.ChatEventActivityStarted, ProviderTurnID: turnID,
						ProviderItemID: id, ActivityKind: domain.ActivityKindMCPTool,
						ActivityStatus: domain.ActivityStatusRunning, Summary: "Subagent response", Detail: detail})
				}
				emit(ports.ChatEvent{Kind: ports.ChatEventActivityText, ProviderTurnID: turnID,
					ProviderItemID: id, Delta: delta})
				break
			}
			c.mu.Lock()
			c.messages[id] += delta
			c.mu.Unlock()
			emit(ports.ChatEvent{Kind: ports.ChatEventMessageDelta, ProviderTurnID: turnID, ProviderItemID: id, Delta: delta})
		}
	case update.AgentThoughtChunk != nil:
		c.mu.Lock()
		isCompacting := c.compactingTurnID != "" && c.compactingTurnID == turnID
		c.mu.Unlock()
		if isCompacting {
			break
		}
		id := c.providerItemID(messageID(update.AgentThoughtChunk.MessageId, "thought", turnID))
		if delta := contentText(update.AgentThoughtChunk.Content); delta != "" {
			c.mu.Lock()
			_, existed := c.thoughts[id]
			c.thoughts[id] += delta
			c.mu.Unlock()
			if !existed {
				emit(ports.ChatEvent{Kind: ports.ChatEventActivityStarted, ProviderTurnID: turnID,
					ProviderItemID: id, ActivityKind: domain.ActivityKindReasoning,
					ActivityStatus: domain.ActivityStatusRunning, Summary: "Reasoning"})
			}
			emit(ports.ChatEvent{Kind: ports.ChatEventReasoningDelta, ProviderTurnID: turnID, ProviderItemID: id, Delta: delta})
		}
	case update.ToolCall != nil:
		tool := &toolState{
			id: string(update.ToolCall.ToolCallId), title: update.ToolCall.Title,
			kind: update.ToolCall.Kind, status: update.ToolCall.Status,
			locations: update.ToolCall.Locations, content: update.ToolCall.Content,
			rawInput: update.ToolCall.RawInput, rawOutput: update.ToolCall.RawOutput,
			meta: cloneMeta(update.ToolCall.Meta),
		}
		c.mu.Lock()
		c.tools[tool.id] = tool
		c.mu.Unlock()
		emit(c.toolEvent(turnID, tool, toolTerminal(tool.status)))
		c.emitDiffs(turnID, tool.id, tool.content, emit)
		c.emitCompaction(tool, turnID, emit)
	case update.ToolCallUpdate != nil:
		tool := c.mergeToolUpdate(update.ToolCallUpdate)
		if delta := terminalOutput(update.ToolCallUpdate.Meta); delta != "" {
			emit(ports.ChatEvent{Kind: ports.ChatEventCommandOutputDelta, ProviderTurnID: turnID,
				ProviderItemID: c.providerItemID(tool.id), Delta: delta})
		}
		emit(c.toolEvent(turnID, tool, toolTerminal(tool.status)))
		c.emitDiffs(turnID, tool.id, tool.content, emit)
		c.emitCompaction(tool, turnID, emit)
	case update.Plan != nil:
		plan := normalizePlan(update.Plan.Entries)
		plan.SpansTurns = c.plansSpanTurns
		emit(ports.ChatEvent{Kind: ports.ChatEventPlanUpdated, ProviderTurnID: turnID, Plan: plan})
	case update.SessionInfoUpdate != nil:
		if update.SessionInfoUpdate.Title != nil {
			emit(ports.ChatEvent{Kind: ports.ChatEventThreadRenamed, Title: *update.SessionInfoUpdate.Title})
		}
		if c.infoContextUsage != nil {
			if used, window, ok := c.infoContextUsage(update.SessionInfoUpdate.Meta); ok {
				emit(ports.ChatEvent{Kind: ports.ChatEventUsage, Usage: &ports.ChatUsage{
					ContextUsed: used, ContextWindow: window, ContextKnown: true,
				}})
			}
		}
		if turnID != "" {
			if event, ok := c.sessionFailureEvent(turnID, sourceID, update.SessionInfoUpdate.Meta); ok {
				emit(event)
			}
		}
	case update.ConfigOptionUpdate != nil:
		// The update is a complete replacement, not a delta. Model changes can
		// rebuild effort and fast-mode choices, including removing an option.
		c.replaceConfigOptions(update.ConfigOptionUpdate.ConfigOptions)
	case update.AvailableCommandsUpdate != nil:
		// Like config options, ACP command updates replace the entire catalog. The
		// provider may discover project commands after session setup or remove one
		// when its configuration changes, so retaining absent entries is wrong.
		c.replaceAvailableCommands(update.AvailableCommandsUpdate.AvailableCommands)
	case update.UsageUpdate != nil:
		c.trackContext(int64(update.UsageUpdate.Used), int64(update.UsageUpdate.Size))
		usage := &ports.ChatUsage{
			ContextUsed: int64(update.UsageUpdate.Used), ContextWindow: int64(update.UsageUpdate.Size),
			ContextKnown: true,
		}
		if update.UsageUpdate.Cost != nil {
			cost := update.UsageUpdate.Cost.Amount
			usage.Cost = &cost
			usage.Currency = update.UsageUpdate.Cost.Currency
		}
		emit(ports.ChatEvent{Kind: ports.ChatEventUsage, Usage: usage})
		if c.usageRateLimits == nil {
			return nil
		}
		if limits := c.usageRateLimits(update.UsageUpdate.Meta); limits != nil {
			emit(ports.ChatEvent{Kind: ports.ChatEventRateLimits, RateLimits: limits})
		}
	}
	return nil
}

func stableACPEventID(meta map[string]any) string {
	eventID, _ := meta[persistenthost.ACPEventIDMetaKey].(string)
	return strings.TrimSpace(eventID)
}

func contentText(content acpsdk.ContentBlock) string {
	if content.Text == nil {
		return ""
	}
	return content.Text.Text
}

func messageID(id *string, prefix, turnID string) string {
	if id != nil && *id != "" {
		return *id
	}
	return prefix + "-" + turnID
}
