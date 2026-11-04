package chat

import (
	"context"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// ArmHandoff is the linearization point for an interface transition. It closes
// source intake and queue dispatch synchronously while sendMu makes promotion
// impossible. Once this method returns, no queued turn accepted before the
// transition can cross the provider seam.
//
// This phase is deliberately reversible and performs no provider or durable
// queue mutation: target preflight can still fail. BeginHandoff settles the queue
// only after that preflight has succeeded.
func (c *Controller) ArmHandoff(
	ctx context.Context,
	policy domain.SessionInterfaceTransitionPolicy,
) error {
	if !policy.Valid() {
		return fmt.Errorf("invalid interface handoff policy %q", policy)
	}
	want := interfaceHandoff(policy)

	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}

	c.mu.Lock()
	if c.handoff == want {
		c.mu.Unlock()
		return nil
	}
	if c.handoff != controllerHandoffNone {
		c.mu.Unlock()
		return ErrControllerHandoff
	}
	c.handoff = want
	c.mu.Unlock()
	return nil
}

// BeginHandoff quiesces a controller whose dispatch gate is already armed. It
// remains safe to call directly: focused callers and tests use this single deep
// interface, while Session Manager calls ArmHandoff synchronously before its
// background preflight and then reaches this method later.
func (c *Controller) BeginHandoff(
	ctx context.Context,
	policy domain.SessionInterfaceTransitionPolicy,
) error {
	if err := c.ArmHandoff(ctx, policy); err != nil {
		return err
	}

	if policy == domain.SessionInterfaceTransitionInterrupt {
		// Target preflight has succeeded. Settle every accepted queue row while
		// sendMu and the armed gate make dispatch and explicit promotion impossible.
		// Local durable state is committed before the external provider side effect.
		c.sendMu.Lock()
		c.mu.Lock()
		active := c.pendingTurnID != ""
		c.mu.Unlock()
		err := c.store.CancelAllQueuedTurns(ctx, c.conversation.ID, c.now())
		c.sendMu.Unlock()
		if err != nil {
			c.AbortHandoff()
			return fmt.Errorf("prepare interrupt handoff: cancel queued turns: %w", err)
		}
		if active {
			if err := c.interruptForHandoff(ctx); err != nil {
				c.AbortHandoff()
				return err
			}
		}
		// Provider acceptance is the source-side cancellation boundary. Some
		// adapters never report turn/completed for a killed long-running tool, so
		// waiting for that notification would make Terminal unreachable again.
		return nil
	}

	c.mu.Lock()
	active := c.pendingTurnID != ""
	c.mu.Unlock()
	if !active {
		// A queued row can exist in the narrow gap after a completion was
		// projected and before its drain ran. Claim it now so drain mode cannot
		// report quiescent while accepted work is still waiting.
		if err := c.drain(ctx); err != nil {
			c.AbortHandoff()
			return fmt.Errorf("drain queued turns before handoff: %w", err)
		}
	}

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		// Quiescence is a dispatch boundary, not merely "pendingTurnID is empty".
		// dispatch marks the durable row running before it installs pendingTurnID;
		// without sendMu a handoff can observe that narrow middle state as neither
		// queued nor active and start the target controller too early.
		c.sendMu.Lock()
		c.mu.Lock()
		busy := c.pendingTurnID != ""
		c.mu.Unlock()
		if !busy {
			_, err := c.store.NextQueuedTurn(ctx, c.conversation.ID)
			switch {
			case errors.Is(err, domain.ErrNoQueuedTurn):
				c.sendMu.Unlock()
				return nil
			case err != nil:
				c.sendMu.Unlock()
				c.AbortHandoff()
				return fmt.Errorf("check queued turns before handoff: %w", err)
			case policy == domain.SessionInterfaceTransitionDrain:
				if err := c.drainLocked(ctx, true); err != nil {
					c.sendMu.Unlock()
					c.AbortHandoff()
					return fmt.Errorf("drain queued turns during handoff: %w", err)
				}
			}
		}
		c.sendMu.Unlock()
		select {
		case <-ctx.Done():
			c.AbortHandoff()
			return ctx.Err()
		case <-c.stopped:
			return nil
		case <-ticker.C:
		}
	}
}

// BeginIdleBranchHandoff installs a persistent intake fence only when the
// controller is already quiescent. Unlike an interface handoff it never drains
// or interrupts accepted work: branch changes are refused until the user stops
// the active turn and the durable queue is empty.
func (c *Controller) BeginIdleBranchHandoff(ctx context.Context) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.handoff != controllerHandoffNone {
		return ErrControllerHandoff
	}
	if c.pendingTurnID != "" {
		return ErrTurnRunning
	}
	if _, err := c.store.NextQueuedTurn(ctx, c.conversation.ID); err == nil {
		return ErrTurnRunning
	} else if !errors.Is(err, domain.ErrNoQueuedTurn) {
		return fmt.Errorf("check queue before branch handoff: %w", err)
	}
	c.handoff = controllerHandoffIdleBranch
	c.branchHandoffDone = make(chan struct{})
	return nil
}

// AbortHandoff reopens source intake when a drain is cancelled before the source
// controller is stopped. Any queued work resumes through the ordinary drain.
func (c *Controller) AbortHandoff() {
	c.mu.Lock()
	resumeDispatch := c.handoff == controllerHandoffInterfaceDrain ||
		c.handoff == controllerHandoffInterfaceInterrupt
	branchHandoffDone := c.branchHandoffDone
	c.branchHandoffDone = nil
	c.handoff = controllerHandoffNone
	c.mu.Unlock()
	if branchHandoffDone != nil {
		close(branchHandoffDone)
	}
	if resumeDispatch {
		go func() {
			_ = c.drain(context.Background()) // drain logs failures; no caller waits on abort.
		}()
	}
}

// completeBranchHandoff releases stopped-controller registry cleanup without
// reopening the obsolete source's intake. Commands may already hold a pointer
// captured before the registry swap, so the fence must remain closed forever.
func (c *Controller) completeBranchHandoff() {
	c.mu.Lock()
	branchHandoffDone := c.branchHandoffDone
	c.branchHandoffDone = nil
	c.mu.Unlock()
	if branchHandoffDone != nil {
		close(branchHandoffDone)
	}
}

// waitForBranchHandoff keeps the registry's stopped-controller cleanup from
// deleting a deliberately closed source while its forked replacement is being
// resumed and installed. Ordinary stops have no latch and return immediately.
func (c *Controller) waitForBranchHandoff() {
	c.mu.Lock()
	done := c.branchHandoffDone
	c.mu.Unlock()
	if done != nil {
		<-done
	}
}

// turnAckWait bounds how long Interrupt waits for the provider to acknowledge a
// turn it has only just been handed.
//
// Stop appears the instant a message is sent, so a user can press it before the
// provider has started the turn — and a provider refuses to cancel a turn it does
// not yet consider active. Waiting out that gap is what makes the button reliable
// in the moment someone is most likely to use it: right after realizing they sent
// the wrong thing.
const turnAckWait = 3 * time.Second

// interruptForHandoff cancels only the provider turn. BeginHandoff has already
// cancelled the durable queue under the dispatch lock, so reusing Interrupt here
// would reintroduce a second, callback-ordered cancellation path.
func (c *Controller) interruptForHandoff(ctx context.Context) error {
	turn, ok := c.awaitAcknowledgedTurn(ctx)
	if !ok {
		return nil
	}
	if err := c.conv.Interrupt(ctx, turn); err != nil {
		if errors.Is(err, ports.ErrChatNoActiveTurn) {
			// Completion won the race after the dispatch gate was armed. The queue is
			// already terminal, so there is no remaining source work to release.
			return nil
		}
		return fmt.Errorf("interrupt turn %s for handoff: %w", turn, err)
	}
	c.mu.Lock()
	c.handoffInterrupted = turn
	c.mu.Unlock()
	return nil
}

// HandoffInterruptedTurn is the provider turn the handoff's interrupt stopped,
// or "" when it stopped none.
func (c *Controller) HandoffInterruptedTurn() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.handoffInterrupted
}

// closeForBranchHandoff retires and terminates a source controller without
// publishing a session-level exit. Branch replacement is an in-place writer
// handoff, not the end of the session; the replacement generation continues the
// lifecycle, but the old persistent host must release exclusive ownership.
func (c *Controller) closeForBranchHandoff(ctx context.Context) error {
	c.prepareBranchHandoffStop()
	return c.Terminate(ctx)
}

func (c *Controller) prepareBranchHandoffStop() {
	c.mu.Lock()
	c.suppressStoppedActivity = true
	c.mu.Unlock()
}

func (c *Controller) reportFailedBranchHandoff(ctx context.Context) {
	c.mu.Lock()
	c.suppressStoppedActivity = false
	stopped := c.state == ports.ChatControllerStopped
	c.mu.Unlock()
	if !stopped {
		// project will publish the ordinary stop boundary when the stream ends.
		return
	}
	reportCtx, cancel := context.WithTimeout(
		context.WithoutCancel(ctx), branchHandoffReportLimit)
	defer cancel()
	c.reportActivity(reportCtx, domain.ActivityExited, "chat.controller.stopped", c.now())
}
