package usage

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/google/uuid"

	"tallpine.eu/tallpine/backend/internal/domain"
)

type codexTokenVector struct {
	InputTokens           int64 `json:"input_tokens"`
	CachedInputTokens     int64 `json:"cached_input_tokens"`
	CacheWriteInputTokens int64 `json:"cache_write_input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
	TotalTokens           int64 `json:"total_tokens"`
}

type codexEnvelope struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

func parseCodex(source domain.UsageSourceContext, records []jsonlRecord, state *codexParserStateV1, result *parseResult) {
	for _, record := range records {
		var envelope codexEnvelope
		if err := json.Unmarshal(record.Data, &envelope); err != nil {
			recordMalformed(result)
			continue
		}
		switch envelope.Type {
		case "session_meta":
			var payload struct {
				ModelProvider string `json:"model_provider"`
			}
			if json.Unmarshal(envelope.Payload, &payload) == nil {
				state.Provider = firstShortTrimmed(payload.ModelProvider, state.Provider)
			}
		case "turn_context":
			var payload struct {
				Model string `json:"model"`
			}
			if json.Unmarshal(envelope.Payload, &payload) == nil {
				state.ModelID = firstShortTrimmed(payload.Model, state.ModelID)
			}
		case "event_msg":
			parseCodexEvent(source, envelope, state, result)
		case "response_item":
			parseCodexResponseItem(envelope.Payload, state, result)
		}
	}
}

func parseCodexResponseItem(raw json.RawMessage, state *codexParserStateV1, result *parseResult) {
	var payload struct {
		Type   string `json:"type"`
		Name   string `json:"name"`
		CallID string `json:"call_id"`
		Output string `json:"output"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return
	}
	switch payload.Type {
	case "function_call":
		if payload.Name != "spawn_agent" {
			return
		}
		if !validCodexCallID(payload.CallID) ||
			(!containsString(state.PendingSpawnCallIDs, payload.CallID) && len(state.PendingSpawnCallIDs) >= maxCodexAttributionIDs) {
			recordMalformed(result)
			return
		}
		state.PendingSpawnCallIDs = appendUniqueString(state.PendingSpawnCallIDs, payload.CallID)
	case "function_call_output":
		if !containsString(state.PendingSpawnCallIDs, payload.CallID) {
			return
		}
		var output struct {
			AgentID string `json:"agent_id"`
		}
		if err := json.Unmarshal([]byte(payload.Output), &output); err != nil || !validCodexAgentID(output.AgentID) {
			recordMalformed(result)
			return
		}
		alreadyDiscovered := containsString(state.DiscoveredChildIDs, output.AgentID)
		if !alreadyDiscovered && len(state.DiscoveredChildIDs) >= maxCodexAttributionIDs {
			recordMalformed(result)
			return
		}
		state.PendingSpawnCallIDs = removeString(state.PendingSpawnCallIDs, payload.CallID)
		if !alreadyDiscovered {
			state.DiscoveredChildIDs = append(state.DiscoveredChildIDs, output.AgentID)
			result.newCodexChild = true
		}
	}
}

func validateCodexDirectParent(source domain.UsageSourceRecord, state *codexParserStateV1) error {
	if state.NativeSessionID != "" && state.NativeSessionID != source.NativeSessionID {
		return errors.New("codex parser state has a mismatched native session")
	}
	if source.SubagentID == "" {
		if state.DirectParentID != "" {
			return errors.New("root source has a direct parent")
		}
		return nil
	}
	if source.SubagentID != source.NativeSessionID ||
		!validCodexAgentID(state.DirectParentID) ||
		state.DirectParentID == source.NativeSessionID {
		return errors.New("invalid child direct parent")
	}
	return nil
}

func codexSessionMetaFromRecord(data []byte) (nativeSessionID, directParentID string, ok bool) {
	var envelope struct {
		Type    string `json:"type"`
		Payload struct {
			ID     string          `json:"id"`
			Source json.RawMessage `json:"source"`
		} `json:"payload"`
	}
	if json.Unmarshal(data, &envelope) != nil || envelope.Type != "session_meta" || envelope.Payload.ID == "" {
		return "", "", false
	}
	directParentID, ok = codexParentThreadIDFromSource(envelope.Payload.Source)
	if !ok {
		return "", "", false
	}
	return envelope.Payload.ID, directParentID, true
}

func codexParentThreadIDFromSource(raw json.RawMessage) (string, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", true
	}
	if raw[0] == '"' {
		var source string
		return "", json.Unmarshal(raw, &source) == nil
	}
	if raw[0] != '{' {
		return "", false
	}
	var source struct {
		Subagent struct {
			ThreadSpawn struct {
				ParentThreadID string `json:"parent_thread_id"`
			} `json:"thread_spawn"`
		} `json:"subagent"`
	}
	if json.Unmarshal(raw, &source) != nil {
		return "", false
	}
	return source.Subagent.ThreadSpawn.ParentThreadID, true
}

func normalizeCodexIDs(values []string, valid func(string) bool) ([]string, error) {
	if len(values) > maxCodexAttributionIDs {
		return nil, errors.New("too many ids")
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if !valid(value) {
			return nil, errors.New("invalid id")
		}
		result = appendUniqueString(result, value)
	}
	return result, nil
}

func validCodexCallID(value string) bool {
	if value == "" || len(value) > maxCodexAttributionIDBytes {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') &&
			(char < '0' || char > '9') && char != '_' && char != '-' {
			return false
		}
	}
	return true
}

func validCodexAgentID(value string) bool {
	if len(value) > maxCodexAttributionIDBytes {
		return false
	}
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == value
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func appendUniqueString(values []string, value string) []string {
	if containsString(values, value) {
		return values
	}
	return append(values, value)
}

func removeString(values []string, target string) []string {
	for index, value := range values {
		if value == target {
			return append(values[:index], values[index+1:]...)
		}
	}
	return values
}

func parseCodexEvent(source domain.UsageSourceContext, envelope codexEnvelope, state *codexParserStateV1, result *parseResult) {
	// payload.info is the bounded object; payload.rate_limits is its sibling and
	// is deliberately never read, so account-level quota state cannot be stored.
	var payload struct {
		Type string          `json:"type"`
		Info json.RawMessage `json:"info"`
	}
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil ||
		payload.Type != "token_count" || !jsonValueReported(payload.Info) {
		return
	}
	var info struct {
		Total              codexTokenVector  `json:"total_token_usage"`
		Last               *codexTokenVector `json:"last_token_usage"`
		ModelContextWindow int64             `json:"model_context_window"`
	}
	if err := json.Unmarshal(payload.Info, &info); err != nil {
		return
	}
	total := info.Total
	if isCodexContextFill(total, info.ModelContextWindow) {
		state.Baseline = codexTokenVector{}
		return
	}
	if !validCodexTotal(total) {
		recordMalformed(result)
		return
	}
	if total.InputTokens < state.Baseline.InputTokens ||
		total.CachedInputTokens < state.Baseline.CachedInputTokens ||
		total.CacheWriteInputTokens < state.Baseline.CacheWriteInputTokens ||
		total.OutputTokens < state.Baseline.OutputTokens ||
		total.ReasoningOutputTokens < state.Baseline.ReasoningOutputTokens ||
		(total.TotalTokens != 0 && state.Baseline.TotalTokens != 0 && total.TotalTokens < state.Baseline.TotalTokens) {
		result.Cursor.AnomalyCount++
		result.Cursor.LastErrorCode = domain.UsageErrorNonMonotonicCumulativeUsage
		state.Baseline = total
		return
	}
	input := total.InputTokens - state.Baseline.InputTokens
	cached := total.CachedInputTokens - state.Baseline.CachedInputTokens
	cacheWrite := total.CacheWriteInputTokens - state.Baseline.CacheWriteInputTokens
	output := total.OutputTokens - state.Baseline.OutputTokens
	reasoning := total.ReasoningOutputTokens - state.Baseline.ReasoningOutputTokens
	reportedTotal := int64(0)
	if total.TotalTokens != 0 {
		if state.Baseline.TotalTokens != 0 {
			reportedTotal = total.TotalTokens - state.Baseline.TotalTokens
		} else {
			reportedTotal = input + output
		}
	}
	delta := codexTokenVector{
		InputTokens: input, CachedInputTokens: cached, CacheWriteInputTokens: cacheWrite,
		OutputTokens: output, ReasoningOutputTokens: reasoning, TotalTokens: reportedTotal,
	}
	selected := delta
	if info.Last != nil {
		last := *info.Last
		if !validCodexTotal(last) {
			result.Cursor.AnomalyCount++
			result.Cursor.LastErrorCode = domain.UsageErrorNonMonotonicCumulativeUsage
			state.Baseline = total
			return
		}
		if !codexVectorMatchesDelta(last, input, cached, cacheWrite, output, reasoning, reportedTotal) {
			result.Cursor.AnomalyCount++
			result.Cursor.LastErrorCode = domain.UsageErrorNonMonotonicCumulativeUsage
			selected = last
		} else if selected.TotalTokens == 0 && last.TotalTokens != 0 {
			selected.TotalTokens = last.TotalTokens
		}
	}
	if selected.InputTokens == 0 && selected.CachedInputTokens == 0 && selected.OutputTokens == 0 &&
		selected.CacheWriteInputTokens == 0 && selected.ReasoningOutputTokens == 0 {
		state.Baseline = total
		return
	}
	tokens, ok := normalizeOpenAIUsage(
		selected.InputTokens,
		selected.CachedInputTokens,
		selected.CacheWriteInputTokens,
		selected.OutputTokens,
	)
	if !ok {
		result.Cursor.AnomalyCount++
		result.Cursor.LastErrorCode = domain.UsageErrorNonMonotonicCumulativeUsage
		return
	}
	// A record that carried no per-event vector still gives up its cache-write
	// count, because the neutral counters beside it are the same subtraction of
	// the same two native readings. Persisting it is what keeps a write-rated
	// model priceable; see codexProviderUsage.
	var derivedCacheWrite *int64
	if info.Last == nil {
		write := selected.CacheWriteInputTokens
		derivedCacheWrite = &write
	}
	state.Baseline = total
	model := firstShortTrimmed(state.ModelID, source.InitialModelID, "unknown")
	state.ModelID = model
	event := domain.ModelUsageEvent{
		ProviderID:            domain.UsageProviderOpenAI,
		BillingProviderID:     canonicalBillingProvider(state.Provider),
		BillingProviderSource: domain.ObservedBillingProviderSource(canonicalBillingProvider(state.Provider)),
		ModelID:               model,
		MeasurementKind:       domain.UsageMeasurementNativeReported,
		Tokens:                tokens,
		ProviderUsageJSON:     codexProviderUsage(payload.Info, derivedCacheWrite),
		CreatedAt:             parseUsageTimestamp(envelope.Timestamp),
		SourceEventKey: stableSourceEventKey(
			"codex",
			source.NativeRootID,
			source.Source.NativeSessionID,
			envelope.Timestamp,
			strconv.FormatInt(total.InputTokens, 10),
			strconv.FormatInt(total.CachedInputTokens, 10),
			strconv.FormatInt(total.CacheWriteInputTokens, 10),
			strconv.FormatInt(total.OutputTokens, 10),
			strconv.FormatInt(total.ReasoningOutputTokens, 10),
		),
	}
	result.Events = append(result.Events, event)
}

func recordMalformed(result *parseResult) {
	result.Cursor.AnomalyCount++
	result.Cursor.LastErrorCode = domain.UsageErrorMalformedJSONL
}

func validCodexTotal(total codexTokenVector) bool {
	if total.InputTokens < 0 || total.CachedInputTokens < 0 || total.CacheWriteInputTokens < 0 ||
		total.OutputTokens < 0 || total.ReasoningOutputTokens < 0 || total.TotalTokens < 0 {
		return false
	}
	if total.CachedInputTokens > total.InputTokens ||
		total.CacheWriteInputTokens > total.InputTokens-total.CachedInputTokens {
		return false
	}
	return total.ReasoningOutputTokens <= total.OutputTokens &&
		(total.TotalTokens == 0 || total.TotalTokens == total.InputTokens+total.OutputTokens)
}

func isCodexContextFill(total codexTokenVector, modelContextWindow int64) bool {
	return modelContextWindow > 0 &&
		total.InputTokens == 0 &&
		total.CachedInputTokens == 0 &&
		total.CacheWriteInputTokens == 0 &&
		total.OutputTokens == 0 &&
		total.ReasoningOutputTokens == 0 &&
		total.TotalTokens == modelContextWindow
}
