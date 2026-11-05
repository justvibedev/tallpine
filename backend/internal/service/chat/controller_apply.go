package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func (c *Controller) apply(ctx context.Context, event ports.ChatEvent, historical bool) error {
	now := c.now()

	switch event.Kind {
	case ports.ChatEventTurnStarted:
		return c.applyTurnStarted(ctx, event, historical, now)

	case ports.ChatEventUserMessageCompleted:
		return c.applyUserMessage(ctx, event, now)

	case ports.ChatEventTurnCompleted:
		return c.applyTurnCompleted(ctx, event, historical, now)

	case ports.ChatEventMessageDelta:
		return c.store.AppendAssistantDelta(ctx, c.conversation.ID,
			event.ProviderItemID, event.ProviderTurnID, event.Delta, c.newID(), now)

	case ports.ChatEventMessageCompleted:
		return c.store.SettleAssistantMessage(ctx, c.conversation.ID,
			event.ProviderItemID, event.ProviderTurnID, event.Text, c.newID(), now)

	case ports.ChatEventCommandOutputDelta:
		// Appended to the command's own activity row, not added to the timeline: a
		// row per delta would bury the conversation under one noisy command.
		//
		// A delta whose activity does not exist yet is dropped. The provider can
		// emit output before the item/started that creates the row, and inventing an
		// activity from a delta would mint a timeline entry with no command on it.
		found, err := c.store.AppendCommandOutput(ctx, c.conversation.ID,
			event.ProviderItemID, event.Delta, now)
		if err != nil {
			return err
		}
		if !found {
			c.log.Debug("command output delta had no activity to append to",
				"session", c.sessionID, "item", event.ProviderItemID)
		} else if c.coalescing() {
			c.coalesce.markOutputStreaming(event.ProviderItemID)
		}
		return nil

	case ports.ChatEventTurnDiff:
		return c.applyTurnDiff(ctx, event, now)

	case ports.ChatEventReasoningDelta, ports.ChatEventCommandInput, ports.ChatEventActivityText:
		return c.applyActivityText(ctx, event, now)

	case ports.ChatEventActivityStarted, ports.ChatEventActivityCompleted:
		return c.applyActivity(ctx, event, now)

	case ports.ChatEventPlanUpdated:
		return c.applyPlan(ctx, event, now)

	case ports.ChatEventModelRerouted:
		return c.applyModelRerouted(ctx, event, now)

	case ports.ChatEventAccountChanged:
		return c.applyAccountChanged(ctx, event, now)

	case ports.ChatEventThreadState:
		if event.ThreadState == nil {
			return nil
		}
		return c.applyThreadState(ctx, *event.ThreadState, now)

	case ports.ChatEventAsyncTask:
		// Daemon state, not conversation state: publishCommittedAsyncTask hands it
		// over after the archive row commits.
		return nil

	case ports.ChatEventMCPServers:
		if len(event.MCPServers) == 0 {
			return nil
		}
		return c.applyMCPServers(ctx, event.MCPServers)

	case ports.ChatEventCompacted:
		return c.applyCompacted(ctx, event, now)

	case ports.ChatEventApprovalRequested:
		return c.applyApprovalRequested(ctx, event, now)

	case ports.ChatEventThreadRenamed:
		return c.applyThreadTitle(ctx, event.Title, now)

	case ports.ChatEventApprovalResolved:
		// The provider resolved it, possibly through another client. Mark it so a
		// card still on screen elsewhere stops being actionable.
		detail := event.Detail
		if len(detail) == 0 {
			detail, _ = json.Marshal(map[string]string{"resolvedBy": "provider"})
		}
		return c.store.ResolveApproval(ctx, c.conversation.ID, event.RequestID, string(detail), now)

	case ports.ChatEventInputRequested:
		return c.applyInputRequested(ctx, event, now)

	case ports.ChatEventInputResolved:
		detail := event.Detail
		if len(detail) == 0 {
			detail, _ = json.Marshal(map[string]string{"resolvedBy": "provider"})
		}
		return c.store.ResolveApproval(ctx, c.conversation.ID, event.RequestID, string(detail), now)

	case ports.ChatEventUsage:
		return c.applyUsage(ctx, event, now)

	case ports.ChatEventRateLimits:
		return c.applyRateLimits(ctx, event)

	case ports.ChatEventControllerState:
		if event.ControllerState == ports.ChatControllerStopped {
			return c.cleanupOwnedControllerWork(ctx, now)
		}
		return nil

	case ports.ChatEventError:
		return c.applyError(ctx, event, now)

	default:
		// An event kind this build does not model is archived but not projected.
		return nil
	}
}

func (c *Controller) applyTurnStarted(ctx context.Context, event ports.ChatEvent, historical bool, now time.Time) error {
	c.mu.Lock()
	dispatchingTurnID := c.dispatchingTurnID
	primary := c.primaryTurnStartLocked(event)
	c.mu.Unlock()
	if event.ProviderTurnID != "" {
		rootConversation := event.ProviderConversationID == "" || event.ProviderConversationID == c.conv.ProviderConversationID()
		if dispatchingTurnID != "" && rootConversation {
			if err := c.store.BindTurnToProvider(ctx, dispatchingTurnID, event.ProviderTurnID, now); err != nil {
				return fmt.Errorf("bind early provider-started turn %s: %w", event.ProviderTurnID, err)
			}
		} else {
			// A turn Tallpine dispatched already has a row, bound in dispatch. This covers
			// the turn Tallpine did NOT dispatch: a compaction, or work the provider resumed
			// from its own history. Adopting it is what keeps every item it emits
			// correlated, and without that the activities arrive with no turn and the
			// timeline quietly stops grouping them.
			var adoptErr error
			if c.reviewID == "" {
				adoptErr = c.store.AdoptProviderTurn(ctx, c.conversation.ID, c.sessionID, c.generation, c.newID(), event.ProviderTurnID, now)
			} else {
				adoptErr = c.store.AdoptReviewProviderTurn(ctx, c.conversation.ID, c.sessionID, c.reviewID, c.generation, c.newID(), event.ProviderTurnID, now)
			}
			if adoptErr != nil {
				return fmt.Errorf("adopt provider-started turn %s: %w", event.ProviderTurnID, adoptErr)
			}
		}

		// The accepted live start, its archive, and report ownership commit
		// together even when adoption has no original Tallpine user-message row.
		if !historical && primary && c.reviewID == "" {
			if err := c.store.RecordActiveChatTurnFallback(ctx, c.conversation.ID, event.ProviderTurnID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *Controller) applyUserMessage(ctx context.Context, event ports.ChatEvent, now time.Time) error {
	if event.ProviderTurnID == "" || event.Text == "" {
		return nil
	}
	return c.store.AppendImportedUserMessage(ctx, c.conversation.ID, event.ProviderTurnID,
		domain.ConversationMessage{
			ID:              c.newID(),
			Role:            domain.MessageRoleUser,
			Origin:          domain.MessageOriginHuman,
			Text:            event.Text,
			ProviderItemID:  event.ProviderItemID,
			ClientMessageID: event.ClientMessageID,
		}, now)
}

func (c *Controller) applyTurnCompleted(ctx context.Context, event ports.ChatEvent, historical bool, now time.Time) error {
	message := ""
	if event.Err != nil {
		message = event.Err.Error()
	}
	state := settledTurnState(event)
	if state == domain.TurnStateInterrupted && message == "" && c.parkCut.Load() {
		message = ports.ErrChatAppRestart.Error()
	}
	c.reauthCleared.Store(false)
	if err := c.store.SettleTurn(
		ctx, c.conversation.ID, event.ProviderTurnID, state, message, now); err != nil {
		return err
	}
	// Use the same root/pending ownership boundary as the post-commit
	// lifecycle effect, but persist the intent inside this transaction. Native
	// history projection is not a fresh completion; live journal recovery is.
	c.mu.Lock()
	primary := c.pendingTurnID == event.ProviderTurnID && event.ProviderTurnID != "" &&
		(event.ProviderConversationID == "" || event.ProviderConversationID == c.conv.ProviderConversationID())
	c.mu.Unlock()
	// Unexpected failures also end the turn while the provider may remain
	// ready. Authentication waits and intentional interruptions do not.
	reportable := state == domain.TurnStateCompleted ||
		(state == domain.TurnStateFailed && !errors.Is(event.Err, ports.ErrChatAuthRequired))
	if !historical && primary && reportable && c.reviewID == "" {
		excerpt := c.finalAssistant.peek(event.ProviderTurnID)
		if state == domain.TurnStateFailed {
			if limit := domain.ParseConversationUsageLimit(message, &now); limit != nil {
				excerpt = domain.UsageLimitFallbackExcerpt(limit.PausedUntil(now), c.autoResumes(limit))
			}
		}
		if err := c.store.RecordChatTurnFallback(ctx, c.conversation.ID, event.ProviderTurnID, excerpt, now); err != nil {
			return err
		}
	}
	if errors.Is(event.Err, ports.ErrChatAuthRequired) {
		if err := c.recordAccount(ctx, ports.ChatAccount{
			ReauthRequired: true,
			ReauthReason:   message,
		}, now); err != nil {
			return err
		}
	} else if state == domain.TurnStateCompleted && event.Err == nil && event.ProviderTurnID != "" &&
		(event.ProviderConversationID == "" || event.ProviderConversationID == c.conv.ProviderConversationID()) {
		previous, err := c.store.ConversationAccount(ctx, c.conversation.ID)
		if err != nil {
			return err
		}
		account, err := c.store.ReconcileConversationAuthentication(ctx, c.conversation.ID, c.generation, event.ProviderTurnID, now)
		if err != nil {
			return err
		}
		// Only a completion that cleared an outstanding demand is recovery the
		// account catalog needs to hear about.
		c.reauthCleared.Store(previous != nil && previous.ReauthRequiredAt != nil &&
			account != nil && account.ReauthRequiredAt == nil)
	}
	return nil
}

func (c *Controller) applyTurnDiff(ctx context.Context, event ports.ChatEvent, now time.Time) error {
	// Per-turn state, overwritten. The provider re-sends the whole diff on every
	// update, so appending a timeline row per notification would show the same
	// edits over and over as if they had happened repeatedly.
	if event.Diff == nil {
		return nil
	}
	diff := domain.ConversationTurnDiff{
		Truncated: event.Summary == domain.ChatDiffTruncatedSummary,
		Files:     make([]domain.ConversationDiffFile, 0, len(event.Diff.Files)),
	}
	for _, file := range event.Diff.Files {
		diff.Files = append(diff.Files, domain.ConversationDiffFile{
			Path:      file.Path,
			Additions: file.Additions,
			Deletions: file.Deletions,
			Status:    file.Status,
			OldPath:   file.OldPath,
		})
	}
	found, err := c.store.SetTurnDiff(ctx, c.conversation.ID, event.ProviderTurnID, diff, now)
	if err != nil {
		return err
	}
	if !found {
		// A turn from before this controller existed, seen after a restart.
		c.log.Debug("turn diff had no turn to attach to",
			"session", c.sessionID, "providerTurn", event.ProviderTurnID)
	}
	return nil
}

func (c *Controller) applyActivityText(ctx context.Context, event ports.ChatEvent, now time.Time) error {
	// Provider prose for one activity, appended to that activity's own stream.
	// Deliberately not a timeline row per delta, and deliberately not the same
	// column as command output: a PTY echoes the keystrokes the agent sends, so
	// merging the two streams would show every typed line twice.
	//
	// A delta whose activity does not exist yet is dropped for the same reason a
	// command output delta is: the provider can stream before the item/started
	// that creates the row, and minting an activity from a delta would put a
	// timeline entry there with nothing on it.
	found, err := c.store.AppendActivityStreamedText(ctx, c.conversation.ID,
		event.ProviderItemID, event.Delta, now)
	if err != nil {
		return err
	}
	if !found {
		c.log.Debug("streamed activity text had no activity to append to",
			"session", c.sessionID, "kind", event.Kind, "item", event.ProviderItemID)
	}
	return nil
}

func (c *Controller) applyActivity(ctx context.Context, event ports.ChatEvent, now time.Time) error {
	if err := c.store.UpsertActivity(ctx, c.conversation.ID, event.ProviderTurnID,
		domain.ConversationActivity{
			ID:             c.newID(),
			Kind:           event.ActivityKind,
			Status:         event.ActivityStatus,
			Summary:        event.Summary,
			Detail:         event.Detail,
			ProviderItemID: event.ProviderItemID,
		}, now); err != nil {
		return err
	}
	// After the upsert, so the row exists to settle. Only when the provider
	// actually settled something: an empty settle would erase an accumulation the
	// user watched arrive, and a reasoning item with an empty summary array is the
	// normal shape on a build that streams no summaries at all.
	if event.Text != "" {
		if _, err := c.store.SettleActivityStreamedText(ctx, c.conversation.ID,
			event.ProviderItemID, event.Text, now); err != nil {
			return err
		}
	}
	return nil
}

func (c *Controller) applyPlan(ctx context.Context, event ports.ChatEvent, now time.Time) error {
	// Per-turn state, overwritten. The provider re-sends the whole plan on every
	// change, so a row per update would read as though the agent had planned three
	// separate times in one turn.
	if event.Plan == nil {
		return nil
	}
	found, err := c.store.SetTurnPlan(ctx, c.conversation.ID, event.ProviderTurnID, *event.Plan)
	if err != nil {
		return err
	}
	if !found {
		// A turn from before this controller existed, seen after a restart.
		c.log.Debug("plan had no turn to attach to",
			"session", c.sessionID, "providerTurn", event.ProviderTurnID)
	}
	// Also a timeline row, upserted so the plan mutates in place rather than
	// stacking. The turn column answers "what is the plan now" without walking the
	// timeline; this row answers "where in the conversation did the agent plan",
	// which the column cannot. The compaction projection already sets this
	// precedent, and the two cannot disagree because both are written from one
	// event.
	return c.store.UpsertActivity(ctx, c.conversation.ID, event.ProviderTurnID,
		domain.ConversationActivity{
			ID:      c.newID(),
			Kind:    domain.ActivityKindPlan,
			Status:  planActivityStatus(*event.Plan),
			Summary: event.Summary,
			Detail:  planDetail(*event.Plan),
			// Synthetic, because turn/plan/updated carries no item id: the plan is
			// not an item. Keyed on the turn so a turn's plan is one row that
			// updates, and prefixed so it can never collide with a provider id.
			ProviderItemID: planItemID(event.ProviderTurnID),
		}, now)
}
