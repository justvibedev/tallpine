package chat

import (
	"context"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// readNativeHistory loads and validates the settled provider thread without
// mutating Tallpine's timeline. A pending provider boundary uses this split phase so
// the caller can project the events inside the same transaction that publishes
// the boundary and controller generation.
func (c *Controller) readNativeHistory(
	ctx context.Context,
	existingTurns []domain.ConversationTurn,
	existingMessages []domain.ConversationMessage,
	existingActivities []domain.ConversationActivity,
	required bool,
	checkpoint nativeHistoryCheckpoint,
) ([]ports.ChatEvent, error) {
	if required && len(checkpoint.hardMismatches) > 0 {
		return nil, fmt.Errorf("native conversation history cannot satisfy durable checkpoint: %w",
			&ports.ChatHistoryUnsettledError{
				Dimensions: append([]ports.ChatHistoryMismatchDimension(nil), checkpoint.hardMismatches...),
			})
	}
	reader, ok := c.conv.(ports.ChatHistoryReader)
	if !ok {
		if required {
			return nil, fmt.Errorf("%w: provider does not implement typed history replay",
				ports.ErrChatHistoryUnavailable)
		}
		return nil, nil
	}
	historyCtx, cancel := context.WithTimeout(ctx, nativeHistorySettleLimit)
	defer cancel()
	events, err := reader.ReadHistory(historyCtx)
	refresher, refreshable := reader.(ports.ChatHistoryRefresher)
	sawUnsettled := false
	var lastUnsettled error
	for refresh := 0; ; refresh++ {
		if err == nil && required {
			if mismatches := checkpoint.mismatches(
				events, existingTurns, existingMessages, existingActivities,
			); len(mismatches) > 0 {
				err = &ports.ChatHistoryUnsettledError{Dimensions: mismatches}
			}
		}
		if err == nil {
			break
		}
		if errors.Is(err, ports.ErrChatHistoryUnavailable) && !required {
			return nil, nil
		}
		if errors.Is(err, ports.ErrChatHistoryLoadFailed) {
			// The provider failed the replay or one attempt hit its bound while
			// the settle budget was still live. Another identical load cannot
			// settle anything, and this must not read as the settle wait's own
			// deadline even though the bounded attempt carries a context error.
			return nil, fmt.Errorf("read native conversation history: %w", err)
		}
		if sawUnsettled && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
			return nil, fmt.Errorf("wait for settled native conversation history: %w: %w",
				lastUnsettled, err)
		}
		if !errors.Is(err, ports.ErrChatHistoryUnsettled) {
			return nil, fmt.Errorf("read native conversation history: %w", err)
		}
		sawUnsettled = true
		lastUnsettled = err
		if !refreshable {
			return nil, fmt.Errorf("native conversation history snapshot is incomplete and cannot be refreshed: %w", err)
		}

		timer := time.NewTimer(nativeHistorySettleDelay(refresh))
		select {
		case <-historyCtx.Done():
			timer.Stop()
			return nil, fmt.Errorf("wait for settled native conversation history: %w: %w",
				lastUnsettled, historyCtx.Err())
		case <-timer.C:
		}
		events, err = refreshNativeHistoryAttempt(historyCtx, refresher)
	}
	for _, event := range events {
		if event.ProviderEventID == "" {
			return nil, fmt.Errorf("native history event %s has no stable identity", event.Kind)
		}
	}
	return events, nil
}

// refreshNativeHistoryAttempt runs one provider re-observation under its own
// deadline, a child of the settle context so parent cancellation still wins.
// Only a timeout the attempt bound caused, while the settle budget is still
// live, is reported as a load failure; the parent's own expiry keeps its
// context error so the caller's settle-wait reporting is unchanged.
func refreshNativeHistoryAttempt(
	parent context.Context,
	refresher ports.ChatHistoryRefresher,
) ([]ports.ChatEvent, error) {
	attemptCtx, cancel := context.WithTimeoutCause(
		parent, nativeHistoryLoadAttemptLimit, errNativeHistoryLoadAttemptTimeout)
	defer cancel()
	events, err := refresher.RefreshHistory(attemptCtx)
	if err != nil && parent.Err() == nil &&
		errors.Is(context.Cause(attemptCtx), errNativeHistoryLoadAttemptTimeout) {
		return nil, fmt.Errorf("%w: %w after %v: %w",
			ports.ErrChatHistoryLoadFailed, errNativeHistoryLoadAttemptTimeout,
			nativeHistoryLoadAttemptLimit, err)
	}
	return events, err
}

// nativeHistorySettleDelay is the pause before the refresh-th provider
// re-observation. Each refresh is a full ACP session/load transcript replay,
// so the poll backs off exponentially from nativeHistorySettlePoll to
// nativeHistorySettlePollMax instead of hammering the provider every 100ms
// for the whole nativeHistorySettleLimit budget. The first retry stays fast
// because a turn that is about to settle usually does so within a beat.
func nativeHistorySettleDelay(refresh int) time.Duration {
	delay := nativeHistorySettlePoll
	for i := 0; i < refresh && delay < nativeHistorySettlePollMax; i++ {
		delay *= 2
	}
	if delay > nativeHistorySettlePollMax {
		delay = nativeHistorySettlePollMax
	}
	return delay
}

// projectNativeHistory durably imports a previously reconciled snapshot.
// Re-running it is safe because history events carry stable identities and
// ProjectProviderEvent deduplicates archive+projection together.
func (c *Controller) projectNativeHistory(ctx context.Context, events []ports.ChatEvent) error {
	for _, event := range events {
		if _, _, err := c.projectEvent(ctx, event, true); err != nil {
			return fmt.Errorf("import native history event %s: %w", event.Kind, err)
		}
	}
	return nil
}
