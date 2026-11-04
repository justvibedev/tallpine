package chat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// afterProject performs effects that intentionally live outside the SQLite
// archive/projection transaction. Lifecycle writes touch the sessions table and
// draining may call the provider, so either one inside the store transaction
// would hold the single writer connection across another subsystem.
func (c *Controller) afterProject(ctx context.Context, event ports.ChatEvent, primaryTurn bool) {
	now := c.now()
	switch event.Kind {
	case ports.ChatEventTurnStarted:
		if primaryTurn {
			c.keepOpenInputsOf(event.ProviderTurnID)
			c.reportActivity(ctx, domain.ActivityActive, "chat.turn.started", now)
		}
	case ports.ChatEventTurnCompleted:
		reauthRequired := errors.Is(event.Err, ports.ErrChatAuthRequired)
		// Recovery is read again too: the catalog may still hold the signed-out
		// reading that the earlier refusal produced.
		if (reauthRequired || c.reauthCleared.Swap(false)) && c.onAccountChanged != nil {
			c.onAccountChanged(c.sessionID, c.generation, c.harness)
		}
		if !primaryTurn {
			return
		}
		// A question the turn left behind can no longer be answered into it.
		c.keepOpenInputsOf("")
		activityState := domain.ActivityIdle
		activityEvent := "chat.turn.completed"
		if reauthRequired {
			activityState = domain.ActivityWaitingInput
			activityEvent = "chat.account.reauth"
		}
		signal := ports.ActivitySignal{
			State: activityState, Event: activityEvent, Timestamp: now,
			FinalAssistantMessage: c.finalAssistant.take(event.ProviderTurnID),
		}
		if event.Err != nil {
			if limit := domain.ParseConversationUsageLimit(event.Err.Error(), &now); limit != nil {
				signal.UsageLimitPausedUntil = limit.PausedUntil(now)
				signal.UsageLimitAutoResume = c.autoResumes(limit)
			}
		}
		c.reportActivitySignal(ctx, signal)
		c.armUsageLimitedTurn(ctx, event, now)
		// Only a completed turn releases queued work; a failed or recovered one holds
		// the queue so it cannot cascade through the same outage (issue #4861).
		_ = c.drainLocked(ctx, settledTurnState(event) == domain.TurnStateCompleted) // drain logs failures.
	case ports.ChatEventAsyncTask:
		c.noteAutonomousTurn(ctx, event.AsyncTask, now)
	case ports.ChatEventApprovalRequested:
		c.reportActivitySignal(ctx, ports.ActivitySignal{
			State: domain.ActivityWaitingInput, Event: "chat.approval.requested", Timestamp: now,
			ToolName: event.Summary, Waiting: approvalWaiting(event.Detail),
			ApprovalCommand: approvalCommand(event.Detail), ApprovalRequestID: event.RequestID,
		})
	case ports.ChatEventApprovalResolved:
		c.reportInteractionResolved(ctx, "chat.approval.resolved", now)
	case ports.ChatEventInputRequested:
		c.trackOpenInput(event.RequestID, event.ProviderTurnID, true)
		c.reportActivitySignal(ctx, ports.ActivitySignal{
			State: domain.ActivityWaitingInput, Event: "chat.input.requested", Timestamp: now,
			Waiting: event.Summary, ApprovalRequestID: event.RequestID,
		})
	case ports.ChatEventInputResolved:
		c.trackOpenInput(event.RequestID, "", false)
		c.reportInteractionResolved(ctx, "chat.input.resolved", now)
	case ports.ChatEventCompacted:
		if event.ProviderConversationID != "" && event.ProviderConversationID != c.conv.ProviderConversationID() {
			return
		}
		c.mu.Lock()
		settled := c.compactionPending && c.pendingTurnID == ""
		if settled {
			c.compactionPending = false
			c.state = ports.ChatControllerReady
		}
		c.mu.Unlock()
		if settled {
			_ = c.drainLocked(ctx, true)
		}
	case ports.ChatEventControllerState:
		// Volatile state moves only after the provider event and all of its durable
		// cleanup committed. Otherwise a rollback can say "stopped" in memory while
		// SQLite still contains live work.
		c.mu.Lock()
		// ACP initialization queues a generic ready notification before live
		// reconnect restores ownership of a durable running turn. That stale
		// notification must not release the reconstructed busy state: only the
		// matching committed turn completion may release pendingTurnID.
		if event.ControllerState != ports.ChatControllerReady || c.pendingTurnID == "" {
			c.state = event.ControllerState
		}
		suppressStoppedActivity := c.suppressStoppedActivity
		c.mu.Unlock()
		if event.ControllerState == ports.ChatControllerStopped && !suppressStoppedActivity {
			c.reportActivity(ctx, domain.ActivityExited, "chat.controller.stopped", now)
		}
	case ports.ChatEventAccountChanged:
		if event.Account != nil && event.Account.ReauthRequired {
			c.reportActivity(ctx, domain.ActivityWaitingInput, "chat.account.reauth", now)
		}
	case ports.ChatEventError:
		if errors.Is(event.Err, ports.ErrChatAuthRequired) {
			c.reportActivity(ctx, domain.ActivityWaitingInput, "chat.account.reauth", now)
			if c.onAccountChanged != nil {
				c.onAccountChanged(c.sessionID, c.generation, c.harness)
			}
		}
	}
}

// approvalWaiting is the agent's stated reason for an approval, else its
// command, as the provider put them in the approval detail.
func approvalWaiting(detail []byte) string {
	var fields struct {
		Reason     string `json:"reason"`
		RawCommand string `json:"rawCommand"`
		Command    string `json:"command"`
	}
	if json.Unmarshal(detail, &fields) != nil {
		return ""
	}
	for _, text := range []string{fields.Reason, fields.RawCommand, fields.Command} {
		if text = strings.TrimSpace(text); text != "" {
			return text
		}
	}
	return ""
}

// maxApprovalCommandRunes bounds the command put into an orchestrator notice.
const maxApprovalCommandRunes = 500

// approvalCommand is the exact command a pending approval would run, bounded.
// Only the command fields are read: an approval's detail can also carry the
// whole input of a Write or Edit (file contents), which must not leave the
// session.
func approvalCommand(detail []byte) string {
	var fields struct {
		RawCommand string `json:"rawCommand"`
		Command    string `json:"command"`
	}
	if json.Unmarshal(detail, &fields) != nil {
		return ""
	}
	command := strings.TrimSpace(fields.RawCommand)
	if command == "" {
		command = strings.TrimSpace(fields.Command)
	}
	if runes := []rune(command); len(runes) > maxApprovalCommandRunes {
		command = string(runes[:maxApprovalCommandRunes]) + "…"
	}
	return command
}

// settledTurnState resolves the only ambiguous terminal combination once. An
// attached error is authoritative failure unless the provider explicitly says
// the user interrupted the turn; an absent state is never evidence of success.
func settledTurnState(event ports.ChatEvent) domain.TurnState {
	if event.TurnState == domain.TurnStateInterrupted {
		return domain.TurnStateInterrupted
	}
	if event.Err != nil || event.TurnState == "" {
		return domain.TurnStateFailed
	}
	return event.TurnState
}

func (c *Controller) reportInteractionResolved(ctx context.Context, event string, now time.Time) {
	pending, err := c.store.HasPendingConversationInteractions(ctx, c.conversation.ID)
	if err != nil {
		// Fail open: staying in waiting-input until the turn ends would hide a
		// resolved card, while a spurious active only lasts until the next signal.
		c.log.Debug("chat pending-interaction check failed; reporting active",
			"session", c.sessionID, "event", event, "error", err)
		pending = false
	}
	if pending {
		return
	}
	c.reportActivity(ctx, domain.ActivityActive, event, now)
}

// applyThreadTitle records a title the provider reports for the thread and, when
// it may, adopts it as the session's label.
//
// The session's display name is the field that already exists for this, so a
// provider title lands there rather than in a parallel one — every surface that
// shows a session name picks it up with no change, and the sessions CDC trigger
// fans it out on the event clients already listen to.
//
// It never overwrites a name a person chose. The store's compare-and-set is what
// enforces that in one statement; a false result here means the user's label won,
// which is the correct outcome and not an error.
//
// A cleared title is recorded but never applied: the provider having no name for
// the thread is not a reason to strip the label off Tallpine's session.
func (c *Controller) applyThreadTitle(ctx context.Context, title string, now time.Time) error {
	normalized := NormalizeTitle(title)
	if err := c.store.SetProviderTitle(ctx, c.conversation.ID, normalized, now); err != nil {
		return err
	}
	if normalized == "" {
		return nil
	}
	if c.reviewID != "" {
		// A reviewer title belongs only to its durable conversation. Applying it
		// through the worker-session CAS would rename the reviewed task.
		return nil
	}
	if c.conversation.Scope == domain.ConversationScopeProject {
		// Only the project orchestrator owns a project conversation, and it is
		// never renamed. Its own agent must not rename it by naming the thread.
		return nil
	}
	applied, err := c.store.ApplyProviderTitle(
		ctx, c.conversation.ID, c.sessionID, normalized, now)
	if err != nil {
		return err
	}
	if !applied {
		c.log.Debug("chat title not applied: session carries a name the user chose",
			"session", c.sessionID)
	}
	return nil
}

// armUsageLimitedTurn gives consent for the auto-resume of a prompt that just
// stopped on a timed usage limit. A worker or orchestrator has nobody to click
// "resume", so without this it would sit idle after the reset until nudged.
// Eligibility is the same check the resume itself repeats; reviewer
// conversations have their own retry path.
func (c *Controller) armUsageLimitedTurn(ctx context.Context, event ports.ChatEvent, now time.Time) {
	// Only sessions with no human at the keyboard arm themselves; an interactive
	// chat keeps the consent button, so an old typed message is never replayed
	// without asking.
	if !unattendedSessionKind(c.kind) {
		return
	}
	if event.Err == nil || event.ProviderTurnID == "" || c.reviewID != "" {
		return
	}
	limit := domain.ParseConversationUsageLimit(event.Err.Error(), &now)
	if limit == nil || limit.ResetsAt == nil {
		return
	}
	store, ok := c.store.(usageLimitStore)
	reader, readerOK := c.store.(interface {
		TurnByProviderID(context.Context, string, string) (domain.ConversationTurn, error)
	})
	if !ok || !readerOK {
		return
	}
	turn, err := reader.TurnByProviderID(ctx, c.conversation.ID, event.ProviderTurnID)
	if err == nil && turn.UsageLimit != nil && turn.UsageLimit.AutoResume {
		return
	}
	if err == nil {
		err = validateUsageLimitSource(ctx, c.store, store, turn, c.sessionID, c.kind)
	}
	if err == nil {
		err = store.SetTurnAutoResume(ctx, c.conversation.ID, turn.ID, limit.ResetsAt, true, "")
	}
	if err != nil {
		// The session stays idle after the reset (B319); make that diagnosable.
		c.log.Warn("usage-limit auto-resume not armed", "session", c.sessionID, "turn", event.ProviderTurnID, "error", err)
	}
}

// autoResumes reports whether armUsageLimitedTurn arms a turn stopped by limit:
// only unattended sessions, and only with a reset time to wait for.
func (c *Controller) autoResumes(limit *domain.ConversationUsageLimit) bool {
	return limit != nil && limit.ResetsAt != nil && unattendedSessionKind(c.kind) && c.reviewID == ""
}
