package review

import (
	"context"
	"fmt"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func (l *agentLauncher) Notify(ctx context.Context, handleID string, spec LaunchSpec) error {
	reviewer, ok := l.reviewers.Reviewer(spec.Harness)
	if !ok {
		return fmt.Errorf("no reviewer adapter for harness %q", spec.Harness)
	}
	inv, err := l.prepareInvocation(ctx, spec)
	if err != nil {
		return err
	}
	msg, err := reviewer.ReviewMessage(ctx, inv)
	if err != nil {
		return fmt.Errorf("reviewer message: %w", err)
	}
	if reviewID, ok := reviewerChatID(handleID); ok && l.chat != nil {
		return l.chat.SendReviewChat(ctx, reviewID, msg, spec.BatchID)
	}
	if err := l.runtime.SendMessage(ctx, ports.RuntimeHandle{ID: handleID}, msg); err != nil {
		return fmt.Errorf("notify reviewer: %w", err)
	}
	return nil
}

func (l *agentLauncher) Alive(ctx context.Context, handleID, launchID string) (bool, error) {
	if handleID == "" {
		return false, nil
	}
	if reviewID, ok := reviewerChatID(handleID); ok && l.chat != nil {
		return l.chat.ReviewChatAlive(reviewID), nil
	}
	if strings.TrimSpace(launchID) != "" {
		handle := ports.RuntimeHandle{ID: handleID}
		ref := ports.SupervisedProcessRef{
			SessionID: domain.SessionID(handleID),
			LaunchID:  launchID,
		}
		tracked, err := l.runtime.HasSupervisedProcessRecord(ctx, handle)
		if err != nil {
			return false, err
		}
		if !tracked {
			if probe, ok := l.runtime.(interface {
				IsUnsupervisedReviewerAlive(context.Context, ports.RuntimeHandle) (bool, error)
			}); ok {
				return probe.IsUnsupervisedReviewerAlive(ctx, handle)
			}
			return l.runtime.IsChildAlive(ctx, handle)
		}
		return l.runtime.IsExactSupervisedProcessAlive(ctx, handle, ref)
	}
	return l.runtime.IsChildAlive(ctx, ports.RuntimeHandle{ID: handleID})
}

// Screen returns the visible tail of the reviewer terminal.
func (l *agentLauncher) Screen(ctx context.Context, handleID string, lines int) (string, error) {
	return l.runtime.GetOutput(ctx, ports.RuntimeHandle{ID: handleID}, lines)
}

func (l *agentLauncher) Reusable(harness domain.ReviewerHarness) bool {
	reviewer, ok := l.reviewers.Reviewer(harness)
	if !ok {
		return false
	}
	policy, ok := reviewer.(ports.ReviewerReusePolicy)
	return !ok || policy.ReviewProcessReusable()
}

func (l *agentLauncher) Cancel(ctx context.Context, handleID string, harness domain.ReviewerHarness) error {
	if handleID == "" {
		return nil
	}
	if reviewID, ok := reviewerChatID(handleID); ok && l.chat != nil {
		// Stop review cancels the turn, just like Stop in the Chat composer.
		// Keep the controller and conversation usable; Archive owns teardown.
		return l.chat.InterruptReviewChat(ctx, reviewID)
	}
	reviewer, ok := l.reviewers.Reviewer(harness)
	if !ok {
		return fmt.Errorf("no reviewer adapter for harness %q", harness)
	}
	canceller, ok := reviewer.(ports.ReviewerCanceller)
	if !ok {
		return fmt.Errorf("reviewer adapter %q does not support cancellation", harness)
	}
	spec, err := canceller.ReviewCancel(ctx)
	if err != nil {
		return fmt.Errorf("reviewer cancel: %w", err)
	}
	switch spec.Mode {
	case ports.ReviewCancelMessage:
		message := strings.TrimSpace(spec.Message)
		if message == "" {
			return fmt.Errorf("reviewer adapter %q returned empty cancel message", harness)
		}
		return l.runtime.SendMessage(ctx, ports.RuntimeHandle{ID: handleID}, message)
	case ports.ReviewCancelInput:
		inputs := spec.Inputs
		if len(inputs) == 0 && spec.Input != "" {
			inputs = []string{spec.Input}
		}
		if len(inputs) == 0 {
			return fmt.Errorf("reviewer adapter %q returned empty cancel input", harness)
		}
		for i, input := range inputs {
			if input == "" {
				return fmt.Errorf("reviewer adapter %q returned empty cancel input", harness)
			}
			if err := l.runtime.SendInput(ctx, ports.RuntimeHandle{ID: handleID}, input); err != nil {
				return err
			}
			if i < len(inputs)-1 && spec.InputDelay > 0 {
				timer := time.NewTimer(spec.InputDelay)
				select {
				case <-ctx.Done():
					timer.Stop()
					return ctx.Err()
				case <-timer.C:
				}
			}
		}
		return nil
	case ports.ReviewCancelInterrupt:
		interrupts := spec.Interrupts
		if interrupts <= 0 {
			interrupts = 1
		}
		for i := 0; i < interrupts; i++ {
			if err := l.runtime.Interrupt(ctx, ports.RuntimeHandle{ID: handleID}); err != nil {
				return err
			}
			if i < interrupts-1 {
				timer := time.NewTimer(cancelInterruptDelay)
				select {
				case <-ctx.Done():
					timer.Stop()
					return ctx.Err()
				case <-timer.C:
				}
			}
		}
		return nil
	default:
		return fmt.Errorf("reviewer adapter %q returned unsupported cancel mode %q", harness, spec.Mode)
	}
}

func (l *agentLauncher) Destroy(ctx context.Context, handleID string) error {
	if handleID == "" {
		return nil
	}
	l.launchedHome.Delete(handleID)
	if reviewID, ok := reviewerChatID(handleID); ok && l.chat != nil {
		return l.chat.StopReviewChat(ctx, reviewID)
	}
	return l.runtime.Destroy(ctx, ports.RuntimeHandle{ID: handleID})
}

func reviewerChatID(handleID string) (string, bool) {
	id, ok := strings.CutPrefix(handleID, reviewerChatHandlePrefix)
	return id, ok && strings.TrimSpace(id) != ""
}

// BatchMessageID ties a dispatched reviewer Chat turn to its durable review runs.
func BatchMessageID(batchID string) string {
	if batchID == "" {
		return ""
	}
	return "review-batch:" + batchID
}
