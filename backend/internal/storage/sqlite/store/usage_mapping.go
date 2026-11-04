package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

func usageBindingFromGen(row gen.UsageBinding) domain.UsageBindingRecord {
	return domain.UsageBindingRecord{
		ID:             row.ID,
		SessionID:      row.SessionID,
		Harness:        row.Harness,
		NativeRootID:   row.NativeRootID,
		InitialModelID: row.InitialModelID,
		ProviderHint:   row.ProviderHint,
		State:          row.State,
		LastErrorCode:  row.LastErrorCode,
		UpdatedAt:      row.UpdatedAt,
	}
}

func usageSourceFromGen(row gen.UsageSource) domain.UsageSourceRecord {
	return domain.UsageSourceRecord{
		ID:              row.ID,
		BindingID:       row.BindingID,
		Kind:            row.Kind,
		NativeSessionID: row.NativeSessionID,
		SubagentID:      row.SubagentID,
		ArtifactPath:    row.ArtifactPath,
		FileIdentity:    row.FileIdentity,
		Generation:      row.Generation,
		ByteOffset:      row.ByteOffset,
		ParserStateJSON: row.ParserStateJson,
		State:           row.State,
		FailureCount:    row.FailureCount,
		AnomalyCount:    row.AnomalyCount,
		NextRetryAt:     nullTimePtr(row.NextRetryAt),
		LastErrorCode:   row.LastErrorCode,
		UpdatedAt:       row.UpdatedAt,
	}
}

func usageSourceContextFromGen(row gen.GetUsageSourceWithBindingAndSessionRow) domain.UsageSourceContext {
	return domain.UsageSourceContext{
		Source: domain.UsageSourceRecord{
			ID:              row.SourceID,
			BindingID:       row.BindingID,
			Kind:            row.Kind,
			NativeSessionID: row.NativeSessionID,
			SubagentID:      row.SubagentID,
			ArtifactPath:    row.ArtifactPath,
			FileIdentity:    row.FileIdentity,
			Generation:      row.Generation,
			ByteOffset:      row.ByteOffset,
			ParserStateJSON: row.ParserStateJson,
			State:           row.SourceState,
			FailureCount:    row.FailureCount,
			AnomalyCount:    row.AnomalyCount,
			NextRetryAt:     nullTimePtr(row.NextRetryAt),
			LastErrorCode:   row.SourceLastErrorCode,
			UpdatedAt:       row.SourceUpdatedAt,
		},
		SessionID:      row.SessionID,
		NativeRootID:   row.NativeRootID,
		InitialModelID: row.InitialModelID,
		ProviderHint:   row.ProviderHint,
		BindingState:   row.BindingState,
	}
}

func usageSourceInsertParams(rec domain.UsageSourceRecord) gen.InsertUsageSourceParams {
	return gen.InsertUsageSourceParams{
		BindingID:       rec.BindingID,
		Kind:            rec.Kind,
		NativeSessionID: rec.NativeSessionID,
		SubagentID:      rec.SubagentID,
		ArtifactPath:    rec.ArtifactPath,
		FileIdentity:    rec.FileIdentity,
		Generation:      rec.Generation,
		ByteOffset:      rec.ByteOffset,
		ParserStateJson: stringOrDefault(rec.ParserStateJSON, "{}"),
		State:           usageSourceStateOrDefault(rec.State),
		FailureCount:    rec.FailureCount,
		AnomalyCount:    rec.AnomalyCount,
		NextRetryAt:     ptrTimeToNullTime(rec.NextRetryAt),
		LastErrorCode:   rec.LastErrorCode,
		UpdatedAt:       timeOrNow(rec.UpdatedAt),
	}
}

func usageEventInsertParams(source gen.GetUsageSourceWithBindingAndSessionRow, ev domain.ModelUsageEvent) gen.InsertModelUsageEventParams {
	return gen.InsertModelUsageEventParams{
		BindingID:             source.BindingID,
		UsageSourceID:         source.SourceID,
		ProviderID:            string(ev.ProviderID),
		BillingProviderID:     stringOrNull(ev.BillingProviderID),
		BillingProviderSource: stringOrNull(string(ev.BillingProviderSource)),
		ModelID:               ev.ModelID,
		UsageMeasurementKind:  string(ev.MeasurementKind),
		InputTokens:           ptrInt64ToNull(ev.Tokens.InputTokens),
		CachedInputTokens:     ptrInt64ToNull(ev.Tokens.CachedInputTokens),
		UncachedInputTokens:   ptrInt64ToNull(ev.Tokens.UncachedInputTokens),
		OutputTokens:          ptrInt64ToNull(ev.Tokens.OutputTokens),
		ProviderUsageJson:     stringOrNull(ev.ProviderUsageJSON),
		InputCostNanos:        ptrInt64ToNull(ev.Costs.InputCostNanos),
		CachedInputCostNanos:  ptrInt64ToNull(ev.Costs.CachedInputCostNanos),
		OutputCostNanos:       ptrInt64ToNull(ev.Costs.OutputCostNanos),
		EstimatedCostNanos:    ptrInt64ToNull(ev.Costs.EstimatedCostNanos),
		PricingVersion:        ev.Costs.PricingVersion,
		SourceEventKey:        ev.SourceEventKey,
		CreatedAt:             sql.NullTime{Time: ev.CreatedAt.UTC(), Valid: !ev.CreatedAt.IsZero()},
	}
}

// stringOrNull maps the empty attribution sentinel to SQL NULL so unattributed
// events stay selectable by the legacy repairer.
func stringOrNull(value string) sql.NullString {
	if value == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: value, Valid: true}
}

// HasOpenUsageAttribution reports whether a source still owns an event whose
// billing attribution a repair pass could finish.
func (s *Store) HasOpenUsageAttribution(ctx context.Context, sourceID int64) (bool, error) {
	open, err := s.qr.HasOpenUsageAttribution(ctx, sourceID)
	if err != nil {
		return false, fmt.Errorf("check open usage attribution for source %d: %w", sourceID, err)
	}
	return open != 0, nil
}

func usageEventReplayDisposition(existing gen.GetModelUsageEventByKeyRow, event domain.ModelUsageEvent) (matches, promoteAttribution bool) {
	genericMatches := existing.ProviderID == string(event.ProviderID) && existing.ModelID == event.ModelID &&
		existing.UsageMeasurementKind == string(event.MeasurementKind) &&
		existing.InputTokens == ptrInt64ToNull(event.Tokens.InputTokens) &&
		existing.CachedInputTokens == ptrInt64ToNull(event.Tokens.CachedInputTokens) &&
		existing.UncachedInputTokens == ptrInt64ToNull(event.Tokens.UncachedInputTokens) &&
		existing.OutputTokens == ptrInt64ToNull(event.Tokens.OutputTokens)
	// A stored object is immutable; a stored NULL predates the capture and the
	// replay is allowed to enrich it.
	if genericMatches && existing.ProviderUsageJson.Valid && event.ProviderUsageJSON != "" {
		genericMatches = existing.ProviderUsageJson.String == event.ProviderUsageJSON
	}
	// A stored row with no billing attribution predates this feature. Keep it
	// token-only comparable so replay never rejects it; the legacy repairer owns
	// filling that column in.
	if !genericMatches || !existing.BillingProviderID.Valid {
		return genericMatches, false
	}
	// An inference is intentionally replaceable by the first observation. That
	// observation also replaces every cost derived from the inferred provider.
	if existing.BillingProviderSource.String == string(domain.UsageBillingProviderInferred) &&
		event.BillingProviderSource == domain.UsageBillingProviderObserved {
		return true, true
	}
	return existing.BillingProviderID.String == event.BillingProviderID, false
}

func usageAggregateFromGen(row gen.AggregateUsageBySessionHarnessModelRow) domain.UsageModelAggregate {
	return domain.UsageModelAggregate{
		Harness: row.Harness,
		ModelID: row.ModelID,
		// A summed metric is only meaningful when every event in the group
		// carried it; one uncollected counter makes the whole sum unknown.
		Tokens: domain.UsageTokenMetrics{
			InputTokens:         int64PtrWhen(row.InputTokens, row.KnownInputTokenCount == row.EventCount),
			CachedInputTokens:   int64PtrWhen(row.CachedInputTokens, row.KnownCachedInputTokenCount == row.EventCount),
			UncachedInputTokens: int64PtrWhen(row.UncachedInputTokens, row.KnownUncachedInputTokenCount == row.EventCount),
			OutputTokens:        int64PtrWhen(row.OutputTokens, row.KnownOutputTokenCount == row.EventCount),
		},
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
	}
}

func validateUsageEvent(harness domain.AgentHarness, event domain.ModelUsageEvent) error {
	expectedProvider := domain.UsageProviderAnthropic
	if harness == domain.HarnessCodex {
		expectedProvider = domain.UsageProviderOpenAI
	}
	if event.ProviderID != expectedProvider || event.ModelID == "" || event.SourceEventKey == "" {
		return fmt.Errorf("invalid usage event identity for %s", harness)
	}
	switch event.MeasurementKind {
	case domain.UsageMeasurementNativeReported, domain.UsageMeasurementTallpineEstimated,
		domain.UsageMeasurementMixed, domain.UsageMeasurementUnknown:
	default:
		return fmt.Errorf("invalid usage measurement kind %q", event.MeasurementKind)
	}
	metrics := event.Tokens
	for _, value := range []*int64{
		metrics.InputTokens, metrics.CachedInputTokens, metrics.UncachedInputTokens, metrics.OutputTokens,
	} {
		if value != nil && *value < 0 {
			return errors.New("usage event tokens must be nonnegative")
		}
	}
	if metrics.InputTokens != nil && metrics.CachedInputTokens != nil && metrics.UncachedInputTokens != nil &&
		*metrics.InputTokens != *metrics.CachedInputTokens+*metrics.UncachedInputTokens {
		return errors.New("usage input does not equal cached plus uncached input")
	}
	// ALTER TABLE cannot add a CHECK that reads another column, so the pairing
	// the schema comment describes is enforced here: a provider without a source
	// would be an attribution nobody can tell apart from an inference, and a
	// source without a provider names nothing.
	switch event.BillingProviderSource {
	case domain.UsageBillingProviderObserved, domain.UsageBillingProviderInferred:
		if event.BillingProviderID == "" {
			return errors.New("usage event billing provider source requires a billing provider")
		}
	case "":
		if event.BillingProviderID != "" {
			return errors.New("usage event billing provider requires a source")
		}
	default:
		return fmt.Errorf("invalid usage billing provider source %q", event.BillingProviderSource)
	}
	if event.ProviderUsageJSON != "" {
		var object map[string]json.RawMessage
		if err := json.Unmarshal([]byte(event.ProviderUsageJSON), &object); err != nil {
			return fmt.Errorf("provider usage must be a JSON object: %w", err)
		}
	}
	return nil
}

func usageBindingStateOrDefault(state domain.UsageBindingState) domain.UsageBindingState {
	if state == "" {
		return domain.UsageBindingDiscovering
	}
	return state
}

func usageSourceStateOrDefault(state domain.UsageSourceState) domain.UsageSourceState {
	if state == "" {
		return domain.UsageSourcePending
	}
	return state
}

func stringOrDefault(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}

func validateParserStateObject(raw string) error {
	if raw == "" {
		raw = "{}"
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &object); err != nil || object == nil {
		return errors.New("usage parser state must be a JSON object")
	}
	return nil
}

func timeOrNow(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now().UTC()
	}
	return t.UTC()
}

func ptrTimeToNullTime(t *time.Time) sql.NullTime {
	if t == nil || t.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t.UTC(), Valid: true}
}

func nullTimePtr(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	out := t.Time
	return &out
}

func ptrInt64ToNull(v *int64) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *v, Valid: true}
}

func nullInt64Ptr(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	value := v.Int64
	return &value
}

func int64PtrWhen(v int64, ok bool) *int64 {
	if !ok {
		return nil
	}
	return &v
}
