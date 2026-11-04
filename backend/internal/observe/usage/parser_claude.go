package usage

import (
	"encoding/json"
	"strconv"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/pricing"
)

type claudeTranscriptRecord struct {
	Type        string `json:"type"`
	UUID        string `json:"uuid"`
	Provider    string `json:"provider"`
	Timestamp   string `json:"timestamp"`
	IsSidechain bool   `json:"isSidechain"`
	Message     struct {
		ID         string  `json:"id"`
		Model      string  `json:"model"`
		Provider   string  `json:"provider"`
		StopReason *string `json:"stop_reason"`
		// Decoded twice on purpose: the typed view drives the neutral counters,
		// and the raw bytes are the bounded provider object stored verbatim so
		// fields Anthropic adds later survive without a schema change here.
		Usage json.RawMessage `json:"usage"`
	} `json:"message"`
}

type claudeNativeUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreation            *struct {
		Ephemeral5mInputTokens int64 `json:"ephemeral_5m_input_tokens"`
		Ephemeral1hInputTokens int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
}

func parseClaude(source domain.UsageSourceContext, records []jsonlRecord, state *claudeParserStateV1, result *parseResult) {
	eventsByKey := make(map[string]domain.ModelUsageEvent)
	for _, record := range records {
		var native claudeTranscriptRecord
		if err := json.Unmarshal(record.Data, &native); err != nil {
			recordMalformed(result)
			continue
		}
		// The transcript feeds the same write-once column as the hook, so it
		// earns the same whitelist. An unrecognised string is dropped rather
		// than stored: unattributed is repairable, a wrong attribution is not.
		nativeProvider := pricing.TrustedClaudeBillingProvider(
			firstShortTrimmed(native.Message.Provider, native.Provider))
		if nativeProvider != "" {
			state.Provider = nativeProvider
		}
		if native.Type != "assistant" || !jsonValueReported(native.Message.Usage) ||
			native.Message.StopReason == nil || strings.TrimSpace(*native.Message.StopReason) == "" {
			continue
		}
		if source.Source.Kind == domain.UsageSourceClaudeMain && native.IsSidechain {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(native.Message.Model), "<synthetic>") {
			continue
		}
		var usage claudeNativeUsage
		if err := json.Unmarshal(native.Message.Usage, &usage); err != nil {
			recordMalformed(result)
			continue
		}
		var creation5m, creation1h *int64
		if usage.CacheCreation != nil {
			creation5m = int64Ptr(usage.CacheCreation.Ephemeral5mInputTokens)
			creation1h = int64Ptr(usage.CacheCreation.Ephemeral1hInputTokens)
		}
		tokens, ok := normalizeAnthropicUsage(
			usage.InputTokens,
			usage.CacheCreationInputTokens,
			usage.CacheReadInputTokens,
			usage.OutputTokens,
			creation5m,
			creation1h,
		)
		if !ok {
			recordMalformed(result)
			continue
		}
		model := firstShortTrimmed(native.Message.Model, state.ModelID, source.InitialModelID, "unknown")
		state.ModelID = model
		billingProvider := pricing.TrustedClaudeBillingProvider(firstShortTrimmed(
			nativeProvider, state.Provider, source.ProviderHint,
		))
		keyID := firstShortTrimmed(native.Message.ID, native.UUID, strconv.FormatInt(record.Offset, 10))
		event := domain.ModelUsageEvent{
			ProviderID:            domain.UsageProviderAnthropic,
			BillingProviderID:     billingProvider,
			BillingProviderSource: domain.ObservedBillingProviderSource(billingProvider),
			ModelID:               model,
			MeasurementKind:       domain.UsageMeasurementNativeReported,
			Tokens:                tokens,
			ProviderUsageJSON:     boundedProviderUsage(native.Message.Usage),
			CreatedAt:             parseUsageTimestamp(native.Timestamp),
			SourceEventKey: stableSourceEventKey(
				"claude",
				source.NativeRootID,
				string(source.Source.Kind),
				source.Source.SubagentID,
				source.Source.NativeSessionID,
				keyID,
			),
		}
		if existing, duplicate := eventsByKey[event.SourceEventKey]; duplicate {
			if !usageEventsEqual(existing, event) {
				result.Cursor.AnomalyCount++
				result.Cursor.LastErrorCode = domain.UsageErrorSourceEventConflict
			}
			continue
		}
		eventsByKey[event.SourceEventKey] = event
		result.Events = append(result.Events, event)
	}
}

// canonicalBillingProvider normalizes one observed routing fact into a catalog
// provider identifier. Unknown or conflicting routing stays empty so the event
// is stored unattributed rather than priced against the wrong provider.
func canonicalBillingProvider(observed string) string {
	provider := pricing.CanonicalProviderID(observed)
	if provider == "unknown" {
		return ""
	}
	return provider
}
