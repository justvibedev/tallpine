package usage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/backoff"
	"tallpine.eu/tallpine/backend/internal/domain"
)

func (i *Ingestor) resetSourceGeneration(
	ctx context.Context,
	source domain.UsageSourceContext,
	identity string,
	now time.Time,
) (IngestResult, error) {
	if source.Source.Kind == domain.UsageSourceCodexRollout {
		return i.retireCodexSourceForReconciliation(ctx, source, now)
	}
	return i.replaceSource(ctx, source, identity, now)
}

func (i *Ingestor) retireCodexSourceForReconciliation(
	ctx context.Context,
	source domain.UsageSourceContext,
	now time.Time,
) (IngestResult, error) {
	result := IngestResult{
		Refresh:       true,
		Reconcile:     true,
		ReconcilePath: source.Source.ArtifactPath,
	}
	if _, err := i.store.MarkUsageSourceState(
		ctx,
		source.Source.ID,
		domain.UsageSourceComplete,
		domain.UsageErrorArtifactReplaced,
		nil,
		now,
	); err != nil {
		return result, err
	}
	if source.BindingState == domain.UsageBindingComplete || source.BindingState == domain.UsageBindingPartial {
		if _, err := i.store.UpdateUsageBindingState(
			ctx,
			source.Source.BindingID,
			domain.UsageBindingFinalizing,
			"",
			now,
		); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (i *Ingestor) replaceSource(
	ctx context.Context,
	source domain.UsageSourceContext,
	identity string,
	now time.Time,
) (IngestResult, error) {
	result := IngestResult{Refresh: true}
	replacement, err := i.store.ReplaceUsageSource(
		ctx,
		source.Source.ID,
		domain.UsageErrorArtifactReplaced,
		domain.UsageSourceRecord{
			BindingID:       source.Source.BindingID,
			Kind:            source.Source.Kind,
			NativeSessionID: source.Source.NativeSessionID,
			SubagentID:      source.Source.SubagentID,
			ArtifactPath:    source.Source.ArtifactPath,
			FileIdentity:    identity,
			Generation:      source.Source.Generation + 1,
			State:           domain.UsageSourcePending,
			UpdatedAt:       now,
		},
		now,
	)
	if err != nil {
		return result, err
	}
	result.ReplacementSourceID = replacement.ID
	if source.BindingState == domain.UsageBindingComplete || source.BindingState == domain.UsageBindingPartial {
		if _, err := i.store.UpdateUsageBindingState(
			ctx,
			source.Source.BindingID,
			domain.UsageBindingFinalizing,
			"",
			now,
		); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (i *Ingestor) retrySource(
	ctx context.Context,
	source domain.UsageSourceRecord,
	code string,
	now time.Time,
	cause error,
) (IngestResult, error) {
	failures := source.FailureCount + 1
	delay := retryDelay(failures)
	next := now.Add(delay)
	result := IngestResult{RetryAt: &next}
	if code == domain.UsageErrorArtifactMissing {
		result.Reconcile = true
	}
	if _, err := i.store.MarkUsageSourceFailure(ctx, source.ID, failures, code, next, now); err != nil {
		return result, err
	}
	if cause == nil {
		cause = errors.New(code)
	}
	return result, fmt.Errorf("usage source %d: %w", source.ID, cause)
}

func (i *Ingestor) completeBinding(ctx context.Context, bindingID int64, now time.Time) error {
	_, err := i.store.CompleteUsageBindingIfSettled(ctx, bindingID, now)
	return err
}

var sourceRetryDelays = []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 5 * time.Minute}

func retryDelay(failure int64) time.Duration {
	return backoff.Table(sourceRetryDelays, int(min(failure, int64(len(sourceRetryDelays)))))
}
