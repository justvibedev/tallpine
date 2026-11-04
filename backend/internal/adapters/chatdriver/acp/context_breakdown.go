package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// contextUsageMethod is Tallpine's request on the patched claude-agent-acp
// (frontend/scripts/build-acp-runtime-helpers.mjs, patchClaudeContextBreakdown).
// A bridge without the patch answers method-not-found.
const contextUsageMethod = "_ao/session/context_usage"

// contextUsageReply is the part of Claude Code's context usage Tallpine reads.
type contextUsageReply struct {
	Model                string `json:"model"`
	TotalTokens          int64  `json:"totalTokens"`
	MaxTokens            int64  `json:"maxTokens"`
	AutoCompactThreshold int64  `json:"autoCompactThreshold"`
	IsAutoCompactEnabled *bool  `json:"isAutoCompactEnabled"`
	Categories           []struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Tokens     int64  `json:"tokens"`
		Kind       string `json:"kind"`
		IsDeferred bool   `json:"isDeferred"`
	} `json:"categories"`
	MemoryFiles []struct {
		Path   string `json:"path"`
		Type   string `json:"type"`
		Tokens int64  `json:"tokens"`
	} `json:"memoryFiles"`
	MessageBreakdown *struct {
		ToolCallTokens         int64 `json:"toolCallTokens"`
		ToolResultTokens       int64 `json:"toolResultTokens"`
		AttachmentTokens       int64 `json:"attachmentTokens"`
		AssistantMessageTokens int64 `json:"assistantMessageTokens"`
		UserMessageTokens      int64 `json:"userMessageTokens"`
	} `json:"messageBreakdown"`
}

// ContextBreakdown asks the bridge for Claude Code's own account of what fills the
// window. Unsupported (an unpatched bridge) and an empty reply read as "no
// breakdown", not as an error.
func (c *conversation) ContextBreakdown(ctx context.Context) (domain.ContextBreakdown, bool, error) {
	c.mu.Lock()
	sessionID := c.sessionID
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return domain.ContextBreakdown{}, false, errConversationClosed
	}
	// The agent counts each category with a model API, which can take a while,
	// and a running turn queues the request behind its own work.
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	raw, err := c.conn.CallExtension(ctx, contextUsageMethod, map[string]any{"sessionId": sessionID})
	if err != nil {
		if isACPMethodNotFound(err) {
			return domain.ContextBreakdown{}, false, nil
		}
		return domain.ContextBreakdown{}, false, fmt.Errorf("ACP %s: %w", contextUsageMethod, err)
	}
	var reply contextUsageReply
	if err := json.Unmarshal(raw, &reply); err != nil {
		return domain.ContextBreakdown{}, false, fmt.Errorf("decode ACP %s response: %w", contextUsageMethod, err)
	}
	return breakdownFromReply(reply)
}

func breakdownFromReply(reply contextUsageReply) (domain.ContextBreakdown, bool, error) {
	if len(reply.Categories) == 0 || reply.MaxTokens <= 0 {
		return domain.ContextBreakdown{}, false, nil
	}
	out := domain.ContextBreakdown{Model: reply.Model, TotalTokens: reply.TotalTokens, MaxTokens: reply.MaxTokens}
	if reply.IsAutoCompactEnabled == nil || *reply.IsAutoCompactEnabled {
		out.AutoCompactThreshold = max(reply.AutoCompactThreshold, 0)
	}
	for _, category := range reply.Categories {
		kind := domain.ContextCategoryKind(category.Kind)
		switch kind {
		case domain.ContextKindUsed, domain.ContextKindFree, domain.ContextKindBuffer, domain.ContextKindDeferred:
		default:
			// An agent that predates row kinds: only the flag it did send is known.
			kind = domain.ContextKindUsed
			if category.IsDeferred {
				kind = domain.ContextKindDeferred
			}
		}
		if category.Tokens < 0 {
			continue
		}
		out.Categories = append(out.Categories, domain.ContextCategory{ID: category.ID, Name: category.Name, Tokens: category.Tokens, Kind: kind})
	}
	if parts := reply.MessageBreakdown; parts != nil {
		out.Messages = &domain.ContextMessageParts{
			ToolCalls: parts.ToolCallTokens, ToolResults: parts.ToolResultTokens, Attachments: parts.AttachmentTokens,
			Assistant: parts.AssistantMessageTokens, User: parts.UserMessageTokens,
		}
	}
	for _, file := range reply.MemoryFiles {
		if file.Tokens > 0 {
			out.MemoryFiles = append(out.MemoryFiles, domain.ContextMemoryFile{Path: file.Path, Type: file.Type, Tokens: file.Tokens})
		}
	}
	return out, true, nil
}
