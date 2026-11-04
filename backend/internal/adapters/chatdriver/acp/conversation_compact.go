package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/google/uuid"

	"tallpine.eu/tallpine/backend/internal/ports"
)

func (c *conversation) Compact(ctx context.Context) (ports.ChatCompactionResult, error) {
	return c.CompactWithInstructions(ctx, "")
}

// CompactWithInstructions compacts like Compact, passing instructions for the
// summary after the command ("/compact <instructions>"), which Claude Code
// honors.
func (c *conversation) CompactWithInstructions(ctx context.Context, instructions string) (ports.ChatCompactionResult, error) {
	if err := ctx.Err(); err != nil {
		return ports.ChatCompactionResult{}, err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ports.ChatCompactionResult{}, errConversationClosed
	}
	if !c.capabilities.Has(ports.ChatCapabilityCompaction) {
		c.mu.Unlock()
		return ports.ChatCompactionResult{}, errors.New("chat driver cannot compact history")
	}
	if c.prepared != nil || c.activeTurn != "" || c.compactingTurnID != "" {
		c.mu.Unlock()
		return ports.ChatCompactionResult{}, errors.New("ACP conversation already has a turn in flight")
	}
	sessionID := c.sessionID
	if sessionID == "" {
		c.mu.Unlock()
		return ports.ChatCompactionResult{}, errors.New("ACP session is not open")
	}
	before := c.contextTokens
	id := uuid.NewString()
	c.activeTurn = id
	c.compactingTurnID = id
	c.compactionBefore = before
	c.compactionSummary = ""
	c.settlingTurn = ""
	turnCtx, cancel := context.WithCancel(context.Background())
	c.turnCancel = cancel
	c.messages = make(map[string]string)
	c.thoughts = make(map[string]string)
	c.nestedMessages = make(map[string]nestedMessageState)
	c.tools = make(map[string]*toolState)
	c.turnDiffs = nil
	c.turnDiffTurnID = ""
	c.providerFailure = nil
	c.mu.Unlock()

	c.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: id})
	c.emit(ports.ChatEvent{Kind: ports.ChatEventControllerState, ControllerState: ports.ChatControllerBusy})

	go c.runCompactionTurn(turnCtx, sessionID, id, compactionCommand(instructions))
	return ports.ChatCompactionResult{TokensBefore: before}, nil
}

// compactionCommand is the /compact prompt, with its instructions when given.
func compactionCommand(instructions string) string {
	if instructions = strings.Join(strings.Fields(instructions), " "); instructions != "" {
		return "/compact " + instructions
	}
	return "/compact"
}

func (c *conversation) runCompactionTurn(ctx context.Context, sessionID, turnID, command string) {
	messageID := uuid.NewString()
	prompt := []acpsdk.ContentBlock{
		acpsdk.TextBlock(command),
	}
	resp, err := c.conn.Prompt(ctx, acpsdk.PromptRequest{
		SessionId: acpsdk.SessionId(sessionID),
		MessageId: &messageID,
		Prompt:    prompt,
	})

	c.finishPrompt(turnID, resp, err)
}

func (c *conversation) settleCompaction(turnID string) {
	c.mu.Lock()
	before := c.compactionBefore
	after := c.contextTokens
	window := c.contextWindow
	summary := c.compactionSummary
	c.compactingTurnID = ""
	c.compactionBefore = 0
	c.compactionSummary = ""
	c.compactedTurn = turnID
	c.mu.Unlock()

	if before > after && after > 0 {
		summary = compactionSummary(before, after)
	} else if summary == "" {
		summary = "Compacted the conversation history"
	}

	detail := map[string]any{}
	if before > 0 {
		detail["tokensBefore"] = before
	}
	if after > 0 {
		detail["tokensAfter"] = after
	}
	if before > after && after > 0 {
		detail["tokensReclaimed"] = before - after
	}
	if window > 0 {
		detail["contextWindow"] = window
	}
	var detailBytes []byte
	if encoded, err := json.Marshal(detail); err == nil {
		detailBytes = encoded
	}

	c.emit(ports.ChatEvent{
		Kind:           ports.ChatEventCompacted,
		ProviderTurnID: turnID,
		Summary:        summary,
		Detail:         detailBytes,
	})
}

func (c *conversation) trackContext(used, window int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.contextTokens = used
	if window > 0 {
		c.contextWindow = window
	}
}

func compactionSummary(before, after int64) string {
	if before <= 0 || after <= 0 || after >= before {
		return "Compacted the conversation history"
	}
	return fmt.Sprintf("Compacted history, freeing %s of context", formatTokens(before-after))
}

func formatTokens(tokens int64) string {
	if tokens < 1000 {
		return fmt.Sprintf("%d tokens", tokens)
	}
	return fmt.Sprintf("%.1fk tokens", float64(tokens)/1000)
}

// respUsageHasTokens reports whether a prompt response carried real usage; a
// non-nil but all-zero block (some providers send one) must not suppress the
// provider's own accounting.
func respUsageHasTokens(u *acpsdk.Usage) bool {
	return u != nil && (u.InputTokens != 0 || u.OutputTokens != 0 || u.TotalTokens != 0)
}

// emitSessionUsage emits the provider's cumulative accounting once it has
// advanced past what was last emitted. The first read is synchronous; if the
// provider has not flushed yet, a background retry waits for it so the turn is
// never held up.
func (c *conversation) emitSessionUsage(sessionID string) {
	if c.tryEmitSessionUsage(sessionID) {
		return
	}
	go func() {
		for attempt := 0; attempt < 4; attempt++ {
			time.Sleep(250 * time.Millisecond)
			if c.tryEmitSessionUsage(sessionID) {
				return
			}
		}
	}()
}

func (c *conversation) tryEmitSessionUsage(sessionID string) bool {
	usage := c.sessionUsage(sessionID)
	if usage == nil {
		return false
	}
	c.mu.Lock()
	advanced := usage.TotalTokens > c.lastSessionUsageTotal
	if advanced {
		c.lastSessionUsageTotal = usage.TotalTokens
	}
	c.mu.Unlock()
	if advanced {
		c.emit(ports.ChatEvent{Kind: ports.ChatEventUsage, Usage: usage})
	}
	return advanced
}
