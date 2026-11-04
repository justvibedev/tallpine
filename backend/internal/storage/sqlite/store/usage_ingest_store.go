package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// ApplyUsageChunk atomically writes parsed usage events and advances the source
// cursor/baselines. The cursor never moves unless all event writes commit.
func (s *Store) ApplyUsageChunk(
	ctx context.Context,
	sourceID, expectedOffset int64,
	expectedRevision time.Time,
	nextState domain.SourceCursorState,
	events []domain.ModelUsageEvent,
) error {
	if nextState.ParserStateJSON != "" {
		if err := validateParserStateObject(nextState.ParserStateJSON); err != nil {
			return err
		}
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	err := s.inTx(ctx, "apply usage chunk", func(q *gen.Queries) error {
		source, err := q.GetUsageSourceWithBindingAndSession(ctx, sourceID)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("usage source %d not found", sourceID)
		}
		if err != nil {
			return err
		}
		if source.ByteOffset != expectedOffset {
			return fmt.Errorf("%w: source %d has offset %d, expected %d", domain.ErrUsageSourceOffsetConflict, sourceID, source.ByteOffset, expectedOffset)
		}
		if !source.SourceUpdatedAt.Equal(expectedRevision) ||
			(source.SourceState == domain.UsageSourceComplete && source.SourceLastErrorCode == domain.UsageErrorArtifactReplaced) {
			return fmt.Errorf("%w: source %d changed while its chunk was being read", domain.ErrUsageSourceRevisionConflict, sourceID)
		}
		insertedEvent := false
		for _, ev := range events {
			ev.ModelID = strings.TrimSpace(ev.ModelID)
			ev.BillingProviderID = strings.TrimSpace(ev.BillingProviderID)
			if err := validateUsageEvent(source.Harness, ev); err != nil {
				return err
			}
			existing, err := q.GetModelUsageEventByKey(ctx, gen.GetModelUsageEventByKeyParams{
				BindingID:      source.BindingID,
				SourceEventKey: ev.SourceEventKey,
			})
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err == nil {
				matches, promoteAttribution := usageEventReplayDisposition(existing, ev)
				if !matches {
					return fmt.Errorf("%w: binding %d event %q", domain.ErrUsageSourceEventConflict, source.BindingID, ev.SourceEventKey)
				}
				// A replaced transcript re-emits the same logical event under
				// the same stable key, so this dedup hit can be a row still
				// pointing at the retired generation. Repair skips that
				// generation, so leaving it there strands the attribution.
				if existing.UsageSourceID != sourceID {
					if _, err := q.RehomeOpenUsageEventToReplacementSource(
						ctx,
						gen.RehomeOpenUsageEventToReplacementSourceParams{
							UsageSourceID:         sourceID,
							ID:                    existing.ID,
							ExpectedUsageSourceID: existing.UsageSourceID,
						},
					); err != nil {
						return err
					}
				}
				if promoteAttribution {
					rows, err := q.PromoteInferredUsageEventToObserved(ctx, gen.PromoteInferredUsageEventToObservedParams{
						BillingProviderID:         stringOrNull(ev.BillingProviderID),
						InputCostNanos:            ptrInt64ToNull(ev.Costs.InputCostNanos),
						CachedInputCostNanos:      ptrInt64ToNull(ev.Costs.CachedInputCostNanos),
						OutputCostNanos:           ptrInt64ToNull(ev.Costs.OutputCostNanos),
						EstimatedCostNanos:        ptrInt64ToNull(ev.Costs.EstimatedCostNanos),
						PricingVersion:            ev.Costs.PricingVersion,
						ID:                        existing.ID,
						ExpectedUsageSourceID:     sourceID,
						ExpectedBillingProviderID: existing.BillingProviderID,
					})
					if err != nil {
						return err
					}
					if rows != 1 {
						return fmt.Errorf("%w: binding %d event %q attribution changed", domain.ErrUsageSourceEventConflict, source.BindingID, ev.SourceEventKey)
					}
					insertedEvent = true
				}
				if existing.ProviderUsageJson.Valid || ev.ProviderUsageJSON == "" {
					continue
				}
				rows, err := q.EnrichModelUsageEventProviderUsage(ctx, gen.EnrichModelUsageEventProviderUsageParams{
					ProviderUsageJson: stringOrNull(ev.ProviderUsageJSON),
					ID:                existing.ID,
				})
				if err != nil {
					return err
				}
				if rows > 0 {
					insertedEvent = true
				}
				continue
			}
			if _, err := q.InsertModelUsageEvent(ctx, usageEventInsertParams(source, ev)); err != nil {
				return err
			}
			insertedEvent = true
		}
		if err := q.UpdateUsageSourceCursor(ctx, gen.UpdateUsageSourceCursorParams{
			ID:              sourceID,
			ByteOffset:      nextState.ByteOffset,
			ParserStateJson: stringOrDefault(nextState.ParserStateJSON, source.ParserStateJson),
			State:           usageSourceStateOrDefault(nextState.State),
			FailureCount:    nextState.FailureCount,
			AnomalyCount:    nextState.AnomalyCount,
			NextRetryAt:     ptrTimeToNullTime(nextState.NextRetryAt),
			LastErrorCode:   nextState.LastErrorCode,
			UpdatedAt:       timeOrNow(nextState.UpdatedAt),
		}); err != nil {
			return err
		}
		if insertedEvent {
			return q.TouchUsageBinding(ctx, gen.TouchUsageBindingParams{
				UpdatedAt: timeOrNow(nextState.UpdatedAt),
				ID:        source.BindingID,
			})
		}
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

// ListUsageCostCandidates returns the next stable bounded page of total-null
// events whose exact canonical provider has not been attempted at version.
func (s *Store) ListUsageCostCandidates(
	ctx context.Context,
	billingProviderID, version string,
	afterID int64,
) ([]domain.UsageCostCandidate, error) {
	rows, err := s.qr.ListUsageCostCandidates(ctx, gen.ListUsageCostCandidatesParams{
		BillingProviderID: sql.NullString{String: billingProviderID, Valid: true},
		PricingVersion:    version,
		AfterID:           afterID,
	})
	if err != nil {
		return nil, fmt.Errorf("list %s usage cost candidates: %w", billingProviderID, err)
	}
	out := make([]domain.UsageCostCandidate, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.UsageCostCandidate{
			ID:                row.ID,
			BindingID:         row.BindingID,
			ProviderID:        domain.UsageProviderID(row.ProviderID),
			BillingProviderID: row.BillingProviderID.String,
			ModelID:           row.ModelID,
			MeasurementKind:   domain.UsageMeasurementKind(row.UsageMeasurementKind),
			Tokens: storedUsageTokens(
				row.InputTokens, row.CachedInputTokens,
				row.UncachedInputTokens, row.OutputTokens,
			),
			ProviderUsageJSON: row.ProviderUsageJson.String,
			PricingVersion:    row.PricingVersion,
			SourceEventKey:    row.SourceEventKey,
		})
	}
	return out, nil
}

// storedUsageTokens rebuilds the canonical token vector from one durable row. A
// NULL column is an uncollected metric; a stored zero is a known zero.
func storedUsageTokens(input, cached, uncached, output sql.NullInt64) domain.UsageTokenMetrics {
	return domain.UsageTokenMetrics{
		InputTokens:         nullInt64Ptr(input),
		CachedInputTokens:   nullInt64Ptr(cached),
		UncachedInputTokens: nullInt64Ptr(uncached),
		OutputTokens:        nullInt64Ptr(output),
	}
}

// ApplyUsageCostUpdates applies one bounded candidate page atomically. Stale
// candidates are ignored, and every binding with at least one winning CAS is
// touched exactly once so the existing trigger publishes one CDC invalidation.
func (s *Store) ApplyUsageCostUpdates(
	ctx context.Context,
	updates []domain.UsageCostUpdate,
	at time.Time,
) (int, error) {
	if len(updates) == 0 {
		return 0, nil
	}
	if len(updates) > 256 {
		return 0, fmt.Errorf("usage cost update batch has %d rows, maximum is 256", len(updates))
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return 0, err
	}
	defer s.writeMu.Unlock()
	applied := 0
	err := s.inTx(ctx, "apply usage cost updates", func(q *gen.Queries) error {
		affectedBindings := make(map[int64]struct{})
		for _, update := range updates {
			candidate := update.Candidate
			bindingID, err := q.UpdateUsageCostCandidate(ctx, gen.UpdateUsageCostCandidateParams{
				InputCostNanos:               ptrInt64ToNull(update.Costs.InputCostNanos),
				CachedInputCostNanos:         ptrInt64ToNull(update.Costs.CachedInputCostNanos),
				OutputCostNanos:              ptrInt64ToNull(update.Costs.OutputCostNanos),
				EstimatedCostNanos:           ptrInt64ToNull(update.Costs.EstimatedCostNanos),
				AttemptedPricingVersion:      update.Costs.PricingVersion,
				ID:                           candidate.ID,
				BindingID:                    candidate.BindingID,
				ExpectedBillingProviderID:    stringOrNull(candidate.BillingProviderID),
				ExpectedModelID:              candidate.ModelID,
				ExpectedUsageMeasurementKind: string(candidate.MeasurementKind),
				ExpectedInputTokens:          ptrInt64ToNull(candidate.Tokens.InputTokens),
				ExpectedCachedInputTokens:    ptrInt64ToNull(candidate.Tokens.CachedInputTokens),
				ExpectedUncachedInputTokens:  ptrInt64ToNull(candidate.Tokens.UncachedInputTokens),
				ExpectedOutputTokens:         ptrInt64ToNull(candidate.Tokens.OutputTokens),
				ExpectedProviderUsageJson:    stringOrNull(candidate.ProviderUsageJSON),
				ExpectedSourceEventKey:       candidate.SourceEventKey,
				ExpectedPricingVersion:       candidate.PricingVersion,
			})
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			applied++
			affectedBindings[bindingID] = struct{}{}
		}
		bindingIDs := make([]int64, 0, len(affectedBindings))
		for bindingID := range affectedBindings {
			bindingIDs = append(bindingIDs, bindingID)
		}
		sort.Slice(bindingIDs, func(left, right int) bool { return bindingIDs[left] < bindingIDs[right] })
		for _, bindingID := range bindingIDs {
			if err := q.TouchUsageBinding(ctx, gen.TouchUsageBindingParams{
				UpdatedAt: timeOrNow(at),
				ID:        bindingID,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return applied, nil
}
