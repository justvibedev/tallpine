package usage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/pricing"
)

// tailSettlement is the outcome of settling an unterminated final record.
type tailSettlement struct {
	finalTailSettled   bool
	tailPending        bool
	restartQuietPeriod bool
}

// settleFinalizingTail decides whether the trailing bytes of a finalizing
// source are a stable, parseable record or still being written.
func (i *Ingestor) settleFinalizingTail(source domain.UsageSourceContext, parserState *parserStateEnvelope, chunk *jsonlChunk, finalizationSettled bool) (res tailSettlement) {
	if source.BindingState == domain.UsageBindingFinalizing && chunk.readToEOF && len(chunk.trailing) > 0 {
		observedTail := stableTailObservation(chunk.trailingOffset, chunk.trailing)
		previousTail := parserState.Integrity.StableTail
		switch {
		case !sameStableTail(previousTail, observedTail):
			parserState.Integrity.StableTail = observedTail
			res.tailPending = true
			res.restartQuietPeriod = true
		case !finalizationSettled:
			res.tailPending = true
		case json.Valid(bytes.TrimSpace(chunk.trailing)) || len(bytes.TrimSpace(chunk.trailing)) == 0:
			if tail := bytes.TrimSpace(chunk.trailing); len(tail) > 0 {
				chunk.records = append(chunk.records, jsonlRecord{
					Data:   append([]byte(nil), tail...),
					Offset: chunk.trailingOffset,
				})
			}
			chunk.nextOffset = chunk.fileSize
			chunk.atEOF = true
			parserState.Integrity.StableTail = nil
			res.finalTailSettled = true
		case previousTail.QuietObservations == 0:
			previousTail.QuietObservations = 1
			res.tailPending = true
			res.restartQuietPeriod = true
		default:
			chunk.nextOffset = chunk.fileSize
			chunk.atEOF = true
			chunk.anomalies++
			chunk.errorCode = domain.UsageErrorMalformedJSONL
			parserState.Integrity.StableTail = nil
			res.finalTailSettled = true
		}
	} else {
		parserState.Integrity.StableTail = nil
	}
	return res
}

// applyFinalizationCursor adjusts the cursor of a finalizing source: pending
// tails schedule a retry, a settled EOF completes the source.
func (i *Ingestor) applyFinalizationCursor(source domain.UsageSourceContext, parsed *parseResult, result *IngestResult, chunk *jsonlChunk, tail tailSettlement, finalizationSettled, progressed bool, now time.Time) {
	if source.BindingState == domain.UsageBindingFinalizing && tail.tailPending {
		settleAt := nextFinalizationRetry(now, i.finalizationWait, source.Source.NextRetryAt, tail.restartQuietPeriod)
		parsed.Cursor.NextRetryAt = &settleAt
		result.RetryAt = &settleAt
	} else if source.BindingState == domain.UsageBindingFinalizing &&
		(chunk.atEOF || !progressed) {
		if finalizationSettled && chunk.atEOF && (!progressed || tail.finalTailSettled) {
			parsed.Cursor.State = domain.UsageSourceComplete
			if parsed.pendingCodexSpawnCalls > 0 {
				parsed.Cursor.AnomalyCount++
				parsed.Cursor.LastErrorCode = domain.UsageErrorUnresolvedSpawnCall
			}
		} else {
			settleAt := nextFinalizationRetry(
				now,
				i.finalizationWait,
				source.Source.NextRetryAt,
				progressed,
			)
			parsed.Cursor.NextRetryAt = &settleAt
			result.RetryAt = &settleAt
		}
	}
}

// applyParsedChunk stores the parsed events, pricing them first when a pricing
// snapshot is configured.
func (i *Ingestor) applyParsedChunk(ctx context.Context, source domain.UsageSourceContext, parsed *parseResult) error {
	if i.pricing == nil {
		return i.store.ApplyUsageChunk(ctx, source.Source.ID, source.Source.ByteOffset, source.Source.UpdatedAt, parsed.Cursor, parsed.Events)
	}
	return i.pricing.WithSnapshot(ctx, func(snapshot *pricing.Snapshot) error {
		for index := range parsed.Events {
			// Live ingestion prices only what the transcript or the
			// binding's route hint named outright. Resolving the model
			// against the catalog would turn "one catalog lists this
			// name" into a billed provider and a dollar amount in the
			// same write, for a session whose route may simply not have
			// been recorded yet. Deriving it is the legacy repairer's
			// job, once no hook is coming.
			estimate, estimateErr := snapshot.Estimate(parsed.Events[index])
			if estimateErr != nil {
				parsed.Events[index].Costs.PricingVersion = snapshot.ProviderVersion(parsed.Events[index].BillingProviderID)
				i.onPricingError(fmt.Errorf("estimate usage event %q: %w", parsed.Events[index].SourceEventKey, estimateErr))
				continue
			}
			parsed.Events[index].Costs = domain.UsageEventCosts{
				InputCostNanos:       estimate.InputNanos,
				CachedInputCostNanos: estimate.CachedInputNanos,
				OutputCostNanos:      estimate.OutputNanos,
				EstimatedCostNanos:   estimate.TotalNanos,
				PricingVersion:       estimate.PricingVersion,
			}
		}
		return i.store.ApplyUsageChunk(ctx, source.Source.ID, source.Source.ByteOffset, source.Source.UpdatedAt, parsed.Cursor, parsed.Events)
	})
}

// completeConflictedSource marks a source whose events conflict as complete
// (with the conflict code) and finishes its binding when finalizing.
func (i *Ingestor) completeConflictedSource(ctx context.Context, source domain.UsageSourceContext, err error, now time.Time) error {
	if _, markErr := i.store.MarkUsageSourceState(
		ctx,
		source.Source.ID,
		domain.UsageSourceComplete,
		domain.UsageErrorSourceEventConflict,
		nil,
		now,
	); markErr != nil {
		return errors.Join(err, markErr)
	}
	if source.BindingState == domain.UsageBindingFinalizing {
		return i.completeBinding(ctx, source.Source.BindingID, now)
	}
	return nil
}

// markBindingFinalizing moves a complete or partial binding to finalizing when
// the transcript grew past the stored offset.
func (i *Ingestor) markBindingFinalizing(ctx context.Context, source *domain.UsageSourceContext, size int64, now time.Time) error {
	if (source.BindingState == domain.UsageBindingComplete || source.BindingState == domain.UsageBindingPartial) &&
		size > source.Source.ByteOffset {
		if _, err := i.store.UpdateUsageBindingState(
			ctx,
			source.Source.BindingID,
			domain.UsageBindingFinalizing,
			"",
			now,
		); err != nil {
			return err
		}
		source.BindingState = domain.UsageBindingFinalizing
	}
	return nil
}
