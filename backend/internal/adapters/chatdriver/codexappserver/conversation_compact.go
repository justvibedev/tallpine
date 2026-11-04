package codexappserver

import (
	"context"
	"encoding/json"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// Compact asks the provider to summarize earlier history and reclaim context.
//
// This is what keeps a long session usable: every turn re-sends the conversation,
// so context fills whether or not the user is doing anything unusual, and once it
// is full the thread cannot accept another turn at all. Compaction is the
// difference between a session that works for an hour and one that works for a day.
//
// The provider accepts the request and returns an empty result immediately; the
// work then runs as its own turn and took 9 to 15 seconds in every measured run.
// So this reports what is about to be reclaimed rather than what was: TokensAfter
// stays zero, and the settled figures reach the client as a ChatEventCompacted on
// the timeline. Blocking here would hold the controller's dispatch lock for the
// duration and make the whole conversation unresponsive while it ran.
func (c *conversation) Compact(ctx context.Context) (ports.ChatCompactionResult, error) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()

	c.mu.Lock()
	before := c.contextTokens
	c.mu.Unlock()

	// thread/compact/start takes the thread id and nothing else. Notably NOT a turn
	// id: compaction is a property of the thread, and passing turn-shaped params
	// here is rejected.
	if err := c.conn.request(ctx, "thread/compact/start", map[string]any{
		"threadId": c.thread(),
	}, nil); err != nil {
		return ports.ChatCompactionResult{}, fmt.Errorf("thread/compact/start: %w", err)
	}

	return ports.ChatCompactionResult{TokensBefore: before}, nil
}

// trackContext folds a token-usage report into the conversation's known context
// position.
func (c *conversation) trackContext(n notification) {
	used, window, ok := contextPositionFrom(n)
	if !ok {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.contextTokens = used
	if window > 0 {
		// The provider omits the window on some reports; a remembered one is better
		// than dropping the scale the used figure is measured against.
		c.contextWindow = window
	}
}

// settleCompaction fills in what a compaction reclaimed, or reports false for one
// already accounted for.
//
// The figures are bracketed rather than read off the event because the provider
// reports neither: `thread/compacted` carries only ids, and the contextCompaction
// item carries only its own id. The reduced token figure arrives as an ordinary
// token-usage report between the item starting and completing, so by the time this
// runs the "after" side is already known.
func (c *conversation) settleCompaction(ev ports.ChatEvent) (ports.ChatEvent, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if ev.ProviderTurnID != "" && ev.ProviderTurnID == c.compactedTurn {
		return ports.ChatEvent{}, false
	}
	c.compactedTurn = ev.ProviderTurnID

	before, after, window := c.contextAtTurnStart, c.contextTokens, c.contextWindow
	ev.Summary = compactionSummary(before, after)

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
	if encoded, err := json.Marshal(detail); err == nil {
		ev.Detail = encoded
	}
	return ev, true
}

// compactionSummary labels the reclaim for a timeline row.
//
// It only claims a number it actually has. Tallpine has no context figure until the
// provider reports one, so a compaction right after a restart genuinely does not
// know what it saved, and "reclaimed 0 tokens" would be a lie rather than a gap.
func compactionSummary(before, after int64) string {
	if before <= 0 || after <= 0 || after >= before {
		return "Compacted the conversation history"
	}
	return fmt.Sprintf("Compacted history, freeing %s of context", formatTokens(before-after))
}

// formatTokens renders a token count the way a reader scans it. Exact below a
// thousand, because that is where the digits still mean something.
func formatTokens(tokens int64) string {
	if tokens < 1000 {
		return fmt.Sprintf("%d tokens", tokens)
	}
	return fmt.Sprintf("%.1fk tokens", float64(tokens)/1000)
}
