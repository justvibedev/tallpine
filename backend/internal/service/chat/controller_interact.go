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

// Resolve answers a pending approval. The provider is told first: if it rejects
// the decision, Tallpine must not have already recorded the approval as answered.
func (c *Controller) Resolve(ctx context.Context, requestID string, decision ports.ChatDecision) error {
	if err := c.conv.ResolveRequest(ctx, requestID, decision); err != nil {
		return fmt.Errorf("resolve request %s: %w", requestID, err)
	}
	recorded := map[string]string{"decision": decision.ID}
	if decision.ResolvedBy != "" {
		recorded["resolvedBy"] = decision.ResolvedBy
		if decision.Note != "" {
			recorded["resolvedNote"] = decision.Note
		}
	}
	detail, _ := json.Marshal(recorded)
	if err := c.store.ResolveApproval(
		ctx, c.conversation.ID, requestID, string(detail), c.now()); err != nil {
		return fmt.Errorf("record approval %s: %w", requestID, err)
	}
	c.reportInteractionResolved(ctx, "chat.approval.resolved", c.now())
	return nil
}

// ResolveInput answers a structured form/URL request through the optional driver
// capability. The provider is told first for the same consent reason as an
// approval: Tallpine must not record an answer that the live provider rejected.
func (c *Controller) ResolveInput(
	ctx context.Context,
	requestID string,
	response ports.ChatInputResponse,
) error {
	responder, ok := c.conv.(ports.ChatInputResponder)
	if !ok {
		return fmt.Errorf("%w: structured input", ports.ErrChatUnsupported)
	}
	if err := responder.ResolveInput(ctx, requestID, response); err != nil {
		return fmt.Errorf("resolve input %s: %w", requestID, err)
	}
	detail, _ := json.Marshal(map[string]any{
		"action":  response.Action,
		"content": response.Content,
	})
	if err := c.store.ResolveApproval(
		ctx, c.conversation.ID, requestID, string(detail), c.now()); err != nil {
		return fmt.Errorf("record input %s: %w", requestID, err)
	}
	c.reportInteractionResolved(ctx, "chat.input.resolved", c.now())
	return nil
}

// ErrCompactionUnsupported reports a driver whose provider cannot summarize
// history. Distinct from a failed compaction: "this agent has no way to reclaim
// context" is a permanent answer a client should stop offering, not something to
// retry.
var ErrCompactionUnsupported = errors.New("chat driver cannot compact history")

// ErrCompactionWhileBusy reports a compaction requested while a turn is running.
//
// Refused rather than forwarded because of what the provider does with it:
// `thread/compact/start` mid-turn silently INTERRUPTS the running turn, reports it
// as interrupted, and then compacts. Measured twice against a live app-server.
// Losing work the user is waiting on as a side effect of a housekeeping action is
// not something they should discover afterwards from the timeline, so Tallpine makes them
// stop the turn themselves.
var ErrCompactionWhileBusy = errors.New("cannot compact while a turn is in flight")

// Compact summarizes earlier history to reclaim context.
//
// Without it a long conversation eventually cannot accept another turn at all:
// every turn re-sends the history, so context fills whether or not the user is
// doing anything unusual.
//
// It takes sendMu for the same reason a dispatch does, and the reason matters here:
// the busy check and the provider call have to be one step. Split, a message
// arriving in between would start a turn that the compaction then destroys — the
// exact outcome the check exists to prevent.
//
// The lock is released as soon as the provider accepts. Until its turn-started
// event commits, compactionPending keeps sends queued and idle hibernation out.
func (c *Controller) Compact(ctx context.Context) (ports.ChatCompactionResult, error) {
	return c.compactWith(ctx, "")
}

// compactWith compacts like Compact, passing instructions for the summary to a
// provider that accepts them; any other compacts with its own default.
func (c *Controller) compactWith(ctx context.Context, instructions string) (ports.ChatCompactionResult, error) {
	compactor, ok := c.conv.(ports.ChatCompactor)
	if !ok || !c.Capabilities().Has(ports.ChatCapabilityCompaction) {
		return ports.ChatCompactionResult{}, ErrCompactionUnsupported
	}

	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if c.handoffActive() {
		return ports.ChatCompactionResult{}, ErrControllerHandoff
	}

	if c.busy() {
		return ports.ChatCompactionResult{}, ErrCompactionWhileBusy
	}
	c.mu.Lock()
	c.compactionPending = true
	c.mu.Unlock()
	var (
		result ports.ChatCompactionResult
		err    error
	)
	if instructed, ok := c.conv.(ports.ChatInstructedCompactor); ok && instructions != "" {
		result, err = instructed.CompactWithInstructions(ctx, instructions)
	} else {
		result, err = compactor.Compact(ctx)
	}
	if err != nil {
		c.mu.Lock()
		c.compactionPending = false
		c.mu.Unlock()
	}
	return result, err
}

// Interrupt cancels the in-flight turn, and with it anything queued behind it.
//
// The queue is cancelled because stop is the user's brake: a brake that releases
// the next message instead of stopping would be the opposite of what the button
// says. The cutoff is recorded before the provider call, so a completion that
// races back cannot drain the queue the interrupt was about to cancel.
// Durable state is what the UI renders, so it is also what Stop must be able to
// act on. Two recovery paths reconcile the cases where in-memory tracking and the
// durable turn row disagree:
//
//   - Memory says nothing is running but SQLite has a turn in 'running'. Either a
//     projection failure kept the completion from committing, or Stop landed in
//     the dispatch window after BindTurnToProvider wrote 'running' but before
//     pendingTurnID was set. Taking sendMu first means dispatch has finished (and
//     set pendingTurnID) before the disk is consulted — if a turn appeared while
//     we waited, the normal interrupt path runs instead.
//   - The provider answers ErrChatNoActiveTurn for a turn memory still tracks.
//     The provider is the authority that the work already ended; surfacing a bare
//     "no active turn" would leave the user staring at a Working bar they cannot
//     dismiss. Reconcile the durable row instead.
func (c *Controller) Interrupt(ctx context.Context) error {
	c.mu.Lock()
	c.interrupts++
	if c.forkCancel != nil {
		c.forkCancel()
	}
	c.mu.Unlock()
	defer func() { c.mu.Lock(); c.interrupts--; c.mu.Unlock() }()
	// Commit the current batch before Stop settles the turn. The store keeps any
	// later deltas settled while retaining text that arrives during cancellation.
	c.flushDeltas(ctx)
	// Stop's linearization point is the dispatch lock. A Send that acquired the
	// lock first is existing work Stop should cancel; a Send that arrives after
	// this point waits and is therefore post-Stop work that must survive.
	c.sendMu.Lock()
	cutoff := c.now()
	c.mu.Lock()
	turn := c.pendingTurnID
	if turn != "" {
		// Publish the queue cutoff before releasing sendMu. A completion can then
		// win the provider race, but it cannot drain pre-Stop work as if Stop had
		// never happened.
		c.cancelQueuedAt = cutoff
	}
	c.mu.Unlock()

	if turn == "" {
		providerTurnIDs, err := c.store.ListVisibleRunningTurnProviderIDs(ctx, c.conversation.ID)
		if err != nil {
			c.sendMu.Unlock()
			return fmt.Errorf("check running turns: %w", err)
		}
		if len(providerTurnIDs) == 0 {
			c.sendMu.Unlock()
			return ErrNoActiveTurn
		}
		// The list uses the snapshot's visibility and ordering rules, so its first
		// row is the same running turn whose Working bar the user pressed Stop on.
		providerTurnID := providerTurnIDs[0]
		if err := c.conv.Interrupt(ctx, providerTurnID); err != nil &&
			!errors.Is(err, ports.ErrChatNoActiveTurn) {
			c.log.Warn("provider interrupt during reconciliation failed",
				"session", c.sessionID, "turn", providerTurnID, "error", err)
		}
		err = c.reconcileDurableTurnsLocked(ctx, providerTurnID, providerTurnIDs, cutoff)
		c.sendMu.Unlock()
		return err
	}
	c.sendMu.Unlock()

	// sendMu proved dispatch is complete, but the provider's turn-started event
	// may still be in transit. Wait for this exact turn: if it completes and a
	// post-Stop survivor starts, Stop must not follow ownership to that new turn.
	if !c.awaitTurnAcknowledged(ctx, turn) {
		return nil
	}

	if err := c.conv.Interrupt(ctx, turn); err != nil {
		if errors.Is(err, ports.ErrChatNoActiveTurn) {
			// Serialize durable settlement, memory cleanup, and queue promotion so
			// a message arriving after Stop cannot slip between those steps.
			c.sendMu.Lock()
			defer c.sendMu.Unlock()

			// A committed completion may have won the sendMu race after the
			// provider answered. It already consumed the cutoff and drained the
			// queue, so do not overwrite its terminal state or a successor turn.
			c.mu.Lock()
			stillPending := c.pendingTurnID == turn
			c.mu.Unlock()
			if !stillPending {
				return nil
			}
			providerTurnIDs, listErr := c.store.ListVisibleRunningTurnProviderIDs(ctx, c.conversation.ID)
			if listErr != nil {
				return fmt.Errorf("check running turns after provider refusal: %w", listErr)
			}
			return c.reconcileDurableTurnsLocked(ctx, turn, providerTurnIDs, cutoff)
		}
		// The interrupt did not happen, so the queue it was going to cancel is
		// still the user's to send.
		c.mu.Lock()
		c.cancelQueuedAt = time.Time{}
		c.mu.Unlock()
		return fmt.Errorf("interrupt turn %s: %w", turn, err)
	}
	return nil
}

// reconcileDurableTurnsLocked settles work the controller can no longer cancel
// through the normal path but durable state still shows as running. Every visible
// running row is settled because nested provider turns can coexist with the root;
// leaving any one of them running would leave the same Working bar behind. Callers
// hold sendMu so settlement, memory cleanup, queue cancellation, and survivor
// dispatch are one serialized transition. The cutoff is the original Stop time: a
// prompt submitted while provider cancellation was pending is new work and survives.
func (c *Controller) reconcileDurableTurnsLocked(
	ctx context.Context,
	targetProviderTurnID string,
	visibleProviderTurnIDs []string,
	cutoff time.Time,
) error {
	now := c.now()
	providerTurnIDs := make([]string, 0, len(visibleProviderTurnIDs)+1)
	if targetProviderTurnID != "" {
		providerTurnIDs = append(providerTurnIDs, targetProviderTurnID)
	}
	for _, providerTurnID := range visibleProviderTurnIDs {
		if providerTurnID != "" && providerTurnID != targetProviderTurnID {
			providerTurnIDs = append(providerTurnIDs, providerTurnID)
		}
	}
	for _, providerTurnID := range providerTurnIDs {
		message := ""
		if c.parkCut.Load() {
			message = ports.ErrChatAppRestart.Error()
		}
		if err := c.store.SettleTurn(ctx, c.conversation.ID, providerTurnID,
			domain.TurnStateInterrupted, message, now); err != nil {
			return fmt.Errorf("reconcile durable turn %s: %w", providerTurnID, err)
		}
	}

	c.mu.Lock()
	c.cancelQueuedAt = cutoff
	if c.pendingTurnID == targetProviderTurnID {
		c.pendingTurnID = ""
	}
	if c.ackedTurnID == targetProviderTurnID {
		c.ackedTurnID = ""
	}
	if c.pendingTurnID == "" {
		c.state = ports.ChatControllerReady
	}
	c.mu.Unlock()

	c.reportActivity(ctx, domain.ActivityIdle, "chat.interrupt.reconciled", now)
	// drainLocked consumes cancelQueuedAt, cancels only the pre-Stop queue, and
	// immediately dispatches the oldest surviving post-Stop prompt.
	return c.drainLocked(ctx, true)
}

// awaitAcknowledgedTurn returns the turn to interrupt once the provider has
// confirmed it started, or reports that there is nothing to cancel.
//
// It gives up immediately when no turn is in flight — that is a plain "nothing to
// stop" and must stay fast. It only waits in the narrow window where Tallpine has
// dispatched a turn and the provider has not yet said so. On expiry it returns the
// turn anyway: the provider is the authority on whether it can be cancelled, and
// its refusal is already translated into a typed answer.
func (c *Controller) awaitAcknowledgedTurn(ctx context.Context) (string, bool) {
	c.mu.Lock()
	pending := c.pendingTurnID
	c.mu.Unlock()
	if pending == "" || !c.awaitTurnAcknowledged(ctx, pending) {
		return "", false
	}
	return pending, true
}

// awaitTurnAcknowledged waits only for the named turn. Ownership can move to a
// queued post-Stop survivor while this wait is in progress; following that move
// would interrupt new work the user submitted after pressing Stop.
func (c *Controller) awaitTurnAcknowledged(ctx context.Context, turn string) bool {
	deadline := time.Now().Add(turnAckWait)
	for {
		c.mu.Lock()
		pending, acked := c.pendingTurnID, c.ackedTurnID
		c.mu.Unlock()

		if pending != turn {
			return false
		}
		if acked == turn {
			return true
		}
		if time.Now().After(deadline) {
			c.log.Debug("chat turn acknowledgement wait expired",
				"session", c.sessionID, "turn", turn, "waited", turnAckWait)
			return true
		}

		select {
		case <-ctx.Done():
			return true
		case <-c.stopped:
			return true
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// Rollback discards a turn and everything after it, and reports how many turns went.
//
// Order is deliberate: the provider first, Tallpine's rows second. If the provider
// refuses, Tallpine must not already have hidden anything — a timeline missing turns the
// agent still remembers is the same lie in the other direction. If Tallpine's write then
// fails, the error is returned and logged loudly: the provider call cannot be undone,
// and the raw provider-event archive plus the surviving turn rows are what make the
// disagreement repairable rather than invisible.
//
// The busy check is inside sendMu, which is also what dispatch holds, so a turn
// cannot start between the check and the provider call. That is the difference
// between refusing a rollback and racing one.
func (c *Controller) Rollback(ctx context.Context, turnID string) (int, error) {
	rollbacker, ok := c.conv.(ports.ChatRollbacker)
	if !ok {
		return 0, ErrRollbackUnsupported
	}

	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if c.handoffActive() {
		return 0, ErrControllerHandoff
	}

	if c.busy() {
		// Not a failure and not permanent: the agent is mid-thought, and the same
		// request works the moment it finishes.
		return 0, ErrTurnRunning
	}

	turn, err := c.store.TurnByID(ctx, turnID)
	if err != nil {
		return 0, err
	}
	if turn.ConversationID != c.conversation.ID {
		return 0, fmt.Errorf("%w: turn %s is not in this session's conversation",
			ErrTurnNotRollbackable, turnID)
	}
	if turn.BranchID != "" {
		turnBranch, branchErr := c.store.ConversationBranch(
			ctx, c.conversation.ID, turn.BranchID)
		if branchErr != nil {
			return 0, fmt.Errorf("load rollback turn branch: %w", branchErr)
		}
		activeBranch, branchErr := c.store.ConversationBranch(
			ctx, c.conversation.ID, c.conversation.ActiveBranchID)
		if branchErr != nil {
			return 0, fmt.Errorf("load active rollback branch: %w", branchErr)
		}
		if turnBranch.ProviderScopeID != activeBranch.ProviderScopeID {
			return 0, ErrTurnProviderMismatch
		}
	}
	if turn.ProviderTurnID == "" {
		// The provider never accepted this turn, so it holds no history to discard.
		// Hiding Tallpine's rows anyway would leave the agent remembering more than the
		// timeline shows, which is the failure this whole operation exists to avoid.
		return 0, fmt.Errorf("%w: %s", ErrTurnNotRollbackable, turnID)
	}

	if err := rollbacker.Rollback(ctx, turn.ProviderTurnID); err != nil {
		return 0, classify(fmt.Errorf("roll back turn %s: %w", turnID, err))
	}

	discarded, err := c.store.RollbackTurns(ctx, c.conversation.ID, turnID, c.now())
	if err != nil {
		c.log.Error("chat rollback: provider discarded history but Tallpine rows did not follow",
			"session", c.sessionID, "turn", turnID, "error", err)
		return 0, fmt.Errorf("record rollback of %s: %w", turnID, err)
	}
	return discarded, nil
}
