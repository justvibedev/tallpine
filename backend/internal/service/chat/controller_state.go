package chat

import (
	"context"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// rateLimitReadTimeout bounds the startup quota read. It is a local IPC call, and
// a provider that cannot answer it quickly must not hold up a conversation.
const rateLimitReadTimeout = 10 * time.Second

// readRateLimits seeds the account's quota position when the controller opens.
//
// The provider only pushes account/rateLimits/updated alongside a turn, which is
// too late for the thing this signal is for: a user wants to know they are near a
// wall BEFORE spending a turn discovering it. Reading once at startup closes that
// gap without giving clients a provider RPC to poll.
//
// Off the critical path on purpose, and failure is logged rather than surfaced: a
// conversation whose quota Tallpine could not read is entirely usable, and refusing to
// start one over a missing readout would be a worse outcome than showing no meter.
func (c *Controller) readRateLimits() {
	reporter, ok := c.conv.(ports.ChatUsageReporter)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), rateLimitReadTimeout)
	defer cancel()

	limits, err := reporter.ReadRateLimits(ctx)
	if err != nil {
		c.log.Debug("chat rate limit read failed", "session", c.sessionID, "error", err)
		return
	}
	// Racing a pushed update is benign: both come from the same provider, and these
	// are percentages of a multi-day window that barely move between two reads
	// seconds apart.
	if err := c.store.RecordRateLimits(ctx, c.conversation.ID, domain.ConversationRateLimits{
		PrimaryUsedPercent:       limits.PrimaryUsedPercent,
		SecondaryUsedPercent:     limits.SecondaryUsedPercent,
		PrimaryResetsInSeconds:   limits.PrimaryResetsInSeconds,
		SecondaryResetsInSeconds: limits.SecondaryResetsInSeconds,
		PlanLabel:                limits.PlanLabel,
	}); err != nil {
		c.log.Debug("failed to record chat rate limits", "session", c.sessionID, "error", err)
	}
}

// ProviderConversationID is the handle to persist for resume.
func (c *Controller) ProviderConversationID() string { return c.conv.ProviderConversationID() }

// ConversationID is the durable conversation this controller writes to.
func (c *Controller) ConversationID() string { return c.conversation.ID }

// Generation fences events from a controller that has been replaced.
func (c *Controller) Generation() string { return c.generation }

// State reports controller health.
func (c *Controller) State() ports.ChatControllerState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// Capabilities reports what this session's provider can do, so a client can gate
// a control before drawing it rather than discovering the answer from a refusal.
func (c *Controller) Capabilities() ports.ChatCapabilities {
	return c.conv.Capabilities()
}

// Settings reports the provider choices for the next turn.
func (c *Controller) Settings() domain.ConversationSettings {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.settings
}

// SetSettings records the provider choices for the next turn.
//
// The row is written first: if that fails, the in-memory copy must not move, or a
// restart would silently revert a choice the user watched take effect.
func (c *Controller) SetSettings(ctx context.Context, settings domain.ConversationSettings) error {
	if err := c.store.SetConversationSettings(ctx, c.conversation.ID, settings, c.now()); err != nil {
		return fmt.Errorf("record conversation settings: %w", err)
	}
	c.mu.Lock()
	c.settings = settings
	c.mu.Unlock()
	return nil
}

// turnSettings converts the stored choices into what a driver takes per turn.
func (c *Controller) turnSettings() ports.ChatTurnSettings {
	current := c.Settings()
	return ports.ChatTurnSettings{
		Model:    current.Model,
		Effort:   current.ReasoningEffort,
		Approval: current.ApprovalMode,
		Fast:     current.FastMode,
		Plan:     current.PlanMode,
	}
}

// mergeUsage combines independently reported usage groups. Explicit flags are
// authoritative, including a meaningful zero after compaction. The inference is
// compatibility for existing drivers/tests that predate the flags and report a
// complete non-zero snapshot in one event.
func (c *Controller) mergeUsage(update ports.ChatUsage) domain.ConversationUsage {
	contextKnown := update.ContextKnown
	totalsKnown := update.TotalsKnown
	if !contextKnown && !totalsKnown {
		contextKnown = update.ContextUsed != 0 || update.ContextWindow != 0
		totalsKnown = update.InputTokens != 0 || update.OutputTokens != 0 ||
			update.CachedTokens != 0 || update.TotalTokens != 0
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if contextKnown {
		c.usage.ContextUsed = update.ContextUsed
		c.usage.ContextWindow = update.ContextWindow
	}
	if totalsKnown {
		c.usage.InputTokens = update.InputTokens
		c.usage.OutputTokens = update.OutputTokens
		c.usage.CachedTokens = update.CachedTokens
		c.usage.TotalTokens = update.TotalTokens
	}
	if update.CostUnknown {
		c.usage.Cost = nil
		c.usage.Currency = ""
	} else if update.Cost != nil {
		cost := *update.Cost
		c.usage.Cost = &cost
		c.usage.Currency = update.Currency
	}
	return c.usage
}

func (c *Controller) setSendingTurn(turnID string) {
	c.mu.Lock()
	c.sendingTurnID = turnID
	c.mu.Unlock()
}

func (c *Controller) clearSendingTurn(turnID string) {
	c.mu.Lock()
	if c.sendingTurnID == turnID {
		c.sendingTurnID = ""
	}
	c.mu.Unlock()
}

// DispatchingTurnIDs are the turns Tallpine is handing, or has just handed, to the
// provider, whether still being recorded or already crossing the provider boundary.
// A client must not present one as queued: nothing is ahead of it.
func (c *Controller) DispatchingTurnIDs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return []string{c.sendingTurnID, c.dispatchingTurnID}
}

// trackOpenInput records a structured question the provider asked in
// providerTurnID (open) or forgets one that was answered or withdrawn.
func (c *Controller) trackOpenInput(requestID, providerTurnID string, open bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !open {
		delete(c.openInputs, requestID)
		return
	}
	if c.openInputs == nil {
		c.openInputs = map[string]string{}
	}
	c.openInputs[requestID] = providerTurnID
}

// keepOpenInputsOf drops every open question not asked by providerTurnID.
// A primary turn starting or ending means no other turn's question can still
// block the conversation.
func (c *Controller) keepOpenInputsOf(providerTurnID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for requestID, turn := range c.openInputs {
		if turn != providerTurnID {
			delete(c.openInputs, requestID)
		}
	}
}

// WaitingOnInput reports whether the running turn is blocked on an in-chat
// question the provider asked and nobody has answered yet.
func (c *Controller) WaitingOnInput() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pendingTurnID == "" {
		return false
	}
	for _, turn := range c.openInputs {
		if turn == c.pendingTurnID {
			return true
		}
	}
	return false
}

// busy reports whether a provider turn is in flight.
func (c *Controller) busy() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pendingTurnID != "" || c.compactionPending
}
