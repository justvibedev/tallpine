package chat

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func (c *Controller) applyModelRerouted(ctx context.Context, event ports.ChatEvent, now time.Time) error {
	// A correction to a claim Tallpine has already made. The composer names the model it
	// is sending to, so a substitution nobody recorded leaves every later reading
	// of the turn attributing the answer to a model that did not produce it.
	if event.Reroute == nil || event.Reroute.ToModel == "" {
		return nil
	}
	if err := c.store.RecordModelReroute(ctx, c.conversation.ID,
		domain.ConversationModelReroute{
			FromModel:      event.Reroute.FromModel,
			ToModel:        event.Reroute.ToModel,
			Reason:         event.Reroute.Reason,
			ProviderTurnID: event.ProviderTurnID,
			At:             now,
		}); err != nil {
		return err
	}
	// And a timeline row, because the substitution happened at a point in the
	// conversation: everything above it was answered by one model and everything
	// below by another, and conversation state alone cannot say where the line is.
	return c.store.UpsertActivity(ctx, c.conversation.ID, event.ProviderTurnID,
		domain.ConversationActivity{
			ID:     c.newID(),
			Kind:   domain.ActivityKindSystem,
			Status: domain.ActivityStatusCompleted,
			Summary: fmt.Sprintf("Provider answered with %s instead of %s",
				event.Reroute.ToModel, cmp.Or(event.Reroute.FromModel, "the selected model")),
			Detail:         rerouteDetail(*event.Reroute),
			ProviderItemID: rerouteItemID(event.ProviderTurnID),
		}, now)
}

func (c *Controller) applyAccountChanged(ctx context.Context, event ports.ChatEvent, now time.Time) error {
	if event.Account == nil {
		return nil
	}
	if err := c.applyAccount(ctx, *event.Account, now); err != nil {
		return err
	}
	// A bare recovery report changes nothing here (turn completion owns the
	// durable recovery), so it must not cost an account probe either: ACP
	// drivers can send one after every successful turn.
	if c.onAccountChanged != nil && !uncorrelatedRecovery(*event.Account) {
		c.onAccountChanged(c.sessionID, c.generation, c.harness)
	}
	return nil
}

func (c *Controller) applyCompacted(ctx context.Context, event ports.ChatEvent, now time.Time) error {
	// A fact about the conversation, emitted from a provider-owned turn that Tallpine
	// did not dispatch. Keep that native turn correlation even though the UI
	// renders it as a boundary between user turns: rollback needs to hide the
	// compaction when the provider forgets the turn that produced it.
	//
	// Recorded because it is the one thing the timeline cannot show without a
	// row: after a restart the reclaim figures are gone from memory, and a
	// conversation that silently lost half its history with nothing to mark where
	// reads as if the agent simply forgot.
	if err := c.store.UpsertActivity(ctx, c.conversation.ID, event.ProviderTurnID,
		domain.ConversationActivity{
			ID:     c.newID(),
			Kind:   domain.ActivityKindSystem,
			Status: domain.ActivityStatusCompleted,
			// Falls back to a plain label rather than an empty row: a driver that
			// reports a compaction without a summary still happened.
			Summary: cmp.Or(event.Summary, "Compacted the conversation history"),
			Detail:  compactionDetail(event),
			// The provider's own item id, so a compaction replayed across a
			// reconnect updates the existing row instead of adding a second.
			ProviderItemID: event.ProviderItemID,
		}, now); err != nil {
		return err
	}
	return c.store.MarkCompacted(ctx, c.conversation.ID, now)
}

func (c *Controller) applyApprovalRequested(ctx context.Context, event ports.ChatEvent, now time.Time) error {
	detail := mergeApprovalDetail(event)
	return c.store.UpsertActivity(ctx, c.conversation.ID, event.ProviderTurnID,
		domain.ConversationActivity{
			ID:             c.newID(),
			Kind:           domain.ActivityKindApproval,
			Status:         domain.ActivityStatusPending,
			Summary:        event.Summary,
			Detail:         detail,
			RequestID:      event.RequestID,
			ProviderItemID: event.ProviderItemID,
		}, now)
}

func (c *Controller) applyInputRequested(ctx context.Context, event ports.ChatEvent, now time.Time) error {
	if event.Input == nil {
		return nil
	}
	detail, _ := json.Marshal(map[string]any{
		"inputMode":     event.Input.Mode,
		"message":       event.Input.Message,
		"schema":        event.Input.Schema,
		"url":           event.Input.URL,
		"elicitationId": event.Input.ElicitationID,
	})
	return c.store.UpsertActivity(ctx, c.conversation.ID, event.ProviderTurnID,
		domain.ConversationActivity{
			ID:             c.newID(),
			Kind:           domain.ActivityKindUserInput,
			Status:         domain.ActivityStatusPending,
			Summary:        cmp.Or(event.Input.Message, "The agent needs more information"),
			Detail:         detail,
			RequestID:      event.RequestID,
			ProviderItemID: event.ProviderItemID,
		}, now)
}

func (c *Controller) applyUsage(ctx context.Context, event ports.ChatEvent, now time.Time) error {
	if event.Usage == nil {
		return nil
	}
	if len(event.Usage.ModelUsage) > 0 {
		if err := c.store.AddModelUsage(ctx, c.conversation.ID, event.Usage.ModelUsage, now); err != nil {
			return err
		}
		u := *event.Usage
		if u.InputTokens == 0 && u.OutputTokens == 0 && u.CachedTokens == 0 && u.TotalTokens == 0 &&
			u.ContextUsed == 0 && u.ContextWindow == 0 && !u.ContextKnown && !u.TotalsKnown &&
			u.Cost == nil && !u.CostUnknown {
			return nil // a per-turn increment only: nothing about the current position
		}
	}
	// Overwrites rather than appends. Deliberately not reported as activity
	// either: token accounting arriving is not the agent doing work, and
	// treating it as such would keep a finished session looking busy.
	return c.store.RecordUsage(ctx, c.conversation.ID, c.mergeUsage(*event.Usage))
}

func (c *Controller) applyRateLimits(ctx context.Context, event ports.ChatEvent) error {
	if event.RateLimits == nil {
		return nil
	}
	if c.harness == domain.HarnessCodex {
		// Account capacity, not conversation state: publishCommittedCapacity
		// hands it to the daemon once this archive row commits.
		return nil
	}
	return c.store.RecordRateLimits(ctx, c.conversation.ID, domain.ConversationRateLimits{
		PrimaryUsedPercent:       event.RateLimits.PrimaryUsedPercent,
		SecondaryUsedPercent:     event.RateLimits.SecondaryUsedPercent,
		PrimaryResetsInSeconds:   event.RateLimits.PrimaryResetsInSeconds,
		SecondaryResetsInSeconds: event.RateLimits.SecondaryResetsInSeconds,
		PlanLabel:                event.RateLimits.PlanLabel,
	})
}

func (c *Controller) applyError(ctx context.Context, event ports.ChatEvent, now time.Time) error {
	message := "provider error"
	if event.Err != nil {
		message = event.Err.Error()
	}
	if errors.Is(event.Err, ports.ErrChatAuthRequired) {
		if err := c.recordAccount(ctx, ports.ChatAccount{
			ReauthRequired: true,
			ReauthReason:   message,
		}, now); err != nil {
			return err
		}
	}
	detail, _ := json.Marshal(map[string]string{"error": message})
	return c.store.UpsertActivity(ctx, c.conversation.ID, event.ProviderTurnID,
		domain.ConversationActivity{
			ID:      c.newID(),
			Kind:    domain.ActivityKindError,
			Status:  domain.ActivityStatusFailed,
			Summary: message,
			Detail:  detail,
		}, now)
}
