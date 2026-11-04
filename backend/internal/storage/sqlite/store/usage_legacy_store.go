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

// ListLegacyUsageSources returns source contexts that still own provider-null,
// total-null events. The repairer validates each durable artifact before use.
func (s *Store) ListLegacyUsageSources(ctx context.Context) ([]domain.UsageSourceContext, error) {
	ids, err := s.qr.ListLegacyUsageSourceIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list legacy usage sources: %w", err)
	}
	out := make([]domain.UsageSourceContext, 0, len(ids))
	for _, id := range ids {
		row, err := s.qr.GetUsageSourceWithBindingAndSession(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("get legacy usage source %d: %w", id, err)
		}
		out = append(out, usageSourceContextFromGen(row))
	}
	return out, nil
}

// ListLegacyUsageEvents returns the immutable generic facts that transcript
// attribution must match before provider and split repair is eligible.
func (s *Store) ListLegacyUsageEvents(ctx context.Context, sourceID int64) ([]domain.LegacyUsageEvent, error) {
	rows, err := s.qr.ListLegacyUsageEvents(ctx, sourceID)
	if err != nil {
		return nil, fmt.Errorf("list legacy usage events for source %d: %w", sourceID, err)
	}
	out := make([]domain.LegacyUsageEvent, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.LegacyUsageEvent{
			ID:                    row.ID,
			BindingID:             row.BindingID,
			UsageSourceID:         row.UsageSourceID,
			ProviderID:            domain.UsageProviderID(row.ProviderID),
			BillingProviderID:     row.BillingProviderID.String,
			BillingProviderSource: domain.UsageBillingProviderSource(row.BillingProviderSource.String),
			ModelID:               row.ModelID,
			MeasurementKind:       domain.UsageMeasurementKind(row.UsageMeasurementKind),
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

// ApplyLegacyUsageRepairs atomically CAS-updates one bounded replay result and
// touches each winning binding once. It never writes usage source cursor state.
func (s *Store) ApplyLegacyUsageRepairs(
	ctx context.Context,
	repairs []domain.LegacyUsageRepair,
	at time.Time,
) (int, error) {
	if len(repairs) == 0 {
		return 0, nil
	}
	if len(repairs) > 256 {
		return 0, fmt.Errorf("legacy usage repair batch has %d rows, maximum is 256", len(repairs))
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return 0, err
	}
	defer s.writeMu.Unlock()
	applied := 0
	err := s.inTx(ctx, "apply legacy usage repairs", func(q *gen.Queries) error {
		affectedBindings := make(map[int64]struct{})
		for _, repair := range repairs {
			billingProviderID := strings.TrimSpace(repair.BillingProviderID)
			if billingProviderID == "" {
				return errors.New("legacy usage repair billing provider must be nonempty")
			}
			switch repair.BillingProviderSource {
			case domain.UsageBillingProviderObserved, domain.UsageBillingProviderInferred:
			default:
				return fmt.Errorf("invalid usage billing provider source %q", repair.BillingProviderSource)
			}
			candidate := repair.Candidate
			bindingID, err := q.UpdateLegacyUsageEvent(ctx, gen.UpdateLegacyUsageEventParams{
				BillingProviderID:             sql.NullString{String: billingProviderID, Valid: true},
				BillingProviderSource:         stringOrNull(string(repair.BillingProviderSource)),
				ProviderUsageJson:             stringOrNull(repair.ProviderUsageJSON),
				InputCostNanos:                ptrInt64ToNull(repair.Costs.InputCostNanos),
				CachedInputCostNanos:          ptrInt64ToNull(repair.Costs.CachedInputCostNanos),
				OutputCostNanos:               ptrInt64ToNull(repair.Costs.OutputCostNanos),
				EstimatedCostNanos:            ptrInt64ToNull(repair.Costs.EstimatedCostNanos),
				PricingVersion:                repair.Costs.PricingVersion,
				ID:                            candidate.ID,
				BindingID:                     candidate.BindingID,
				UsageSourceID:                 candidate.UsageSourceID,
				ExpectedBillingProviderID:     stringOrNull(candidate.BillingProviderID),
				ExpectedBillingProviderSource: stringOrNull(string(candidate.BillingProviderSource)),
				ExpectedProviderID:            string(candidate.ProviderID),
				ExpectedModelID:               candidate.ModelID,
				ExpectedUsageMeasurementKind:  string(candidate.MeasurementKind),
				ExpectedInputTokens:           ptrInt64ToNull(candidate.Tokens.InputTokens),
				ExpectedCachedInputTokens:     ptrInt64ToNull(candidate.Tokens.CachedInputTokens),
				ExpectedUncachedInputTokens:   ptrInt64ToNull(candidate.Tokens.UncachedInputTokens),
				ExpectedOutputTokens:          ptrInt64ToNull(candidate.Tokens.OutputTokens),
				ExpectedProviderUsageJson:     stringOrNull(candidate.ProviderUsageJSON),
				ExpectedSourceEventKey:        candidate.SourceEventKey,
				ExpectedPricingVersion:        candidate.PricingVersion,
				ExpectedFileIdentity:          repair.ExpectedFileIdentity,
				ExpectedByteOffset:            repair.ExpectedByteOffset,
				ExpectedParserStateJson:       repair.ExpectedParserStateJSON,
				ExpectedSourceUpdatedAt:       repair.ExpectedSourceUpdatedAt,
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
				UpdatedAt: timeOrNow(at), ID: bindingID,
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

// ListUsageModelAggregates returns model-level aggregate rows for a session.
func (s *Store) ListUsageModelAggregates(ctx context.Context, sessionID domain.SessionID) ([]domain.UsageModelAggregate, error) {
	rows, err := s.qr.AggregateUsageBySessionHarnessModel(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("aggregate usage for session %s: %w", sessionID, err)
	}
	out := make([]domain.UsageModelAggregate, 0, len(rows))
	for _, row := range rows {
		out = append(out, usageAggregateFromGen(row))
	}
	return out, nil
}

// GetUsageSessionIncomplete reports whether durable collection facts indicate missing usage.
func (s *Store) GetUsageSessionIncomplete(ctx context.Context, sessionID domain.SessionID) (bool, error) {
	incomplete, err := s.qr.GetUsageSessionIncomplete(ctx, string(sessionID))
	if err != nil {
		return false, fmt.Errorf("get usage integrity for session %s: %w", sessionID, err)
	}
	return incomplete != 0, nil
}

// ListCompactSessionUsageAggregates returns sessions with observed usage in
// one grouped read. projectID optionally limits the rows to one dashboard board.
func (s *Store) ListCompactSessionUsageAggregates(ctx context.Context, projectID domain.ProjectID) ([]domain.CompactSessionUsageAggregate, error) {
	rows, err := s.qr.ListCompactSessionUsage(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("list compact session usage for project %s: %w", projectID, err)
	}
	out := make([]domain.CompactSessionUsageAggregate, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.CompactSessionUsageAggregate{
			SessionID:       row.SessionID,
			ProcessedTokens: int64PtrWhen(row.ProcessedTokens, row.ProcessedTokensKnown != 0),
			Incomplete:      row.Incomplete != 0,
			Cost: domain.UsageCostAggregate{
				EventCount: row.EventCount, PricedEventCount: row.PricedEventCount, PricedTotalNanos: row.PricedTotalNanos,
				ObservedCostEventCount: row.ObservedCostEventCount, InferredCostEventCount: row.InferredCostEventCount,
				KnownInputCount: row.KnownInputCount, KnownInputNanos: row.KnownInputNanos,
				UnpricedKnownInputNanos: row.UnpricedKnownInputNanos,
				KnownCachedInputCount:   row.KnownCachedInputCount, KnownCachedInputNanos: row.KnownCachedInputNanos,
				UnpricedKnownCachedInputNanos: row.UnpricedKnownCachedInputNanos,
				KnownOutputCount:              row.KnownOutputCount, KnownOutputNanos: row.KnownOutputNanos,
				UnpricedKnownOutputNanos: row.UnpricedKnownOutputNanos,
			},
		})
	}
	return out, nil
}
