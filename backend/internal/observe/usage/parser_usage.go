package usage

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func normalizeOpenAIUsage(input, cachedInput, cacheWriteInput, output int64) (domain.UsageTokenMetrics, bool) {
	if input < 0 || cachedInput < 0 || cachedInput > input || cacheWriteInput < 0 ||
		cacheWriteInput > input-cachedInput || output < 0 {
		return domain.UsageTokenMetrics{}, false
	}
	return domain.UsageTokenMetrics{
		InputTokens:         int64Ptr(input),
		CachedInputTokens:   int64Ptr(cachedInput),
		UncachedInputTokens: int64Ptr(input - cachedInput),
		OutputTokens:        int64Ptr(output),
	}, true
}

func normalizeAnthropicUsage(directInput, cacheCreationInput, cachedInput, output int64, creation5m, creation1h *int64) (domain.UsageTokenMetrics, bool) {
	uncachedInput, ok := sumNonNegative(directInput, cacheCreationInput)
	if !ok {
		return domain.UsageTokenMetrics{}, false
	}
	input, ok := sumNonNegative(cachedInput, uncachedInput)
	if !ok || output < 0 || !validAnthropicCacheCreation(cacheCreationInput, creation5m, creation1h) {
		return domain.UsageTokenMetrics{}, false
	}
	return domain.UsageTokenMetrics{
		InputTokens:         int64Ptr(input),
		CachedInputTokens:   int64Ptr(cachedInput),
		UncachedInputTokens: int64Ptr(uncachedInput),
		OutputTokens:        int64Ptr(output),
	}, true
}

// maxProviderUsageBytes bounds one stored provider usage object. Transcripts are
// untrusted input, and an object this large is no longer the small counter
// record the providers document, so it is dropped rather than persisted.
const maxProviderUsageBytes = 8 << 10

// jsonValueReported reports whether raw carries a value at all, as opposed to
// being absent or an explicit JSON null.
//
// That distinction used to come free from a nil pointer. A json.RawMessage
// holding `null` has nonzero length and decodes into a typed struct as all-zero
// values, so a record reporting no usage would otherwise become a synthetic
// zero event — or, for Codex, a cumulative baseline of zero that recharges
// every earlier token. Callers still decode the value afterwards, so a present
// but non-object member remains malformed rather than being silently dropped.
func jsonValueReported(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

// isJSONObject reports whether raw is a present JSON object. Unmarshalling into
// a map is the check: JSON null yields a nil map without an error, and any
// other JSON type fails outright.
func isJSONObject(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var object map[string]json.RawMessage
	return json.Unmarshal(raw, &object) == nil && object != nil
}

// codexProviderUsage stores the CLI's own usage object, plus the per-event
// cache-write count when the CLI reported only cumulative totals.
//
// The estimator needs a per-event write bucket to price a model that publishes
// a cache-write rate — every current gpt-5.6 variant does — and it reads that
// bucket from last_token_usage, which is optional. On a record without one the
// counters Tallpine stores are already a delta of two native cumulative readings, and
// this is that same subtraction over the same readings, so it is derived rather
// than invented. It is namespaced instead of folded into a last_token_usage the
// CLI never emitted: a reader must still be able to tell what the provider said
// from what Tallpine worked out.
func codexProviderUsage(raw json.RawMessage, derivedCacheWrite *int64) string {
	stored := boundedProviderUsage(raw)
	if stored == "" || derivedCacheWrite == nil {
		return stored
	}
	derived := json.RawMessage(strconv.FormatInt(*derivedCacheWrite, 10))
	object := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(stored), &object); err != nil {
		return stored
	}
	object[derivedCacheWriteKey] = derived
	encoded, err := json.Marshal(object)
	if err != nil || len(encoded) > maxProviderUsageBytes {
		return stored
	}
	return string(encoded)
}

// derivedCacheWriteKey names the one member Tallpine adds to a stored provider usage
// object. The ao_ prefix is the whole point: nothing reading this object should
// mistake it for something a provider reported.
const derivedCacheWriteKey = "ao_derived_cache_write_input_tokens"

// boundedProviderUsage compacts one provider usage object for durable storage.
// It returns empty for anything that is not a JSON object or exceeds the bound,
// which stores SQL NULL: an absent object is honest, a truncated one is not.
func boundedProviderUsage(raw json.RawMessage) string {
	if len(raw) > maxProviderUsageBytes || !isJSONObject(raw) {
		return ""
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return ""
	}
	return compact.String()
}

func validAnthropicCacheCreation(total int64, creation5m, creation1h *int64) bool {
	if creation5m == nil && creation1h == nil {
		return true
	}
	if creation5m == nil || creation1h == nil || *creation5m < 0 || *creation1h < 0 {
		return false
	}
	return *creation5m <= total && *creation1h <= total-*creation5m
}

func codexVectorMatchesDelta(last codexTokenVector, input, cachedInput, cacheWriteInput, output, reasoningOutput, total int64) bool {
	return validCodexTotal(last) && last.InputTokens == input && last.CachedInputTokens == cachedInput &&
		last.CacheWriteInputTokens == cacheWriteInput && last.OutputTokens == output &&
		last.ReasoningOutputTokens == reasoningOutput &&
		(last.TotalTokens == 0 || total == 0 || last.TotalTokens == total)
}

func parseUsageTimestamp(raw string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}

func usageEventsEqual(a, b domain.ModelUsageEvent) bool {
	a.CreatedAt, b.CreatedAt = time.Time{}, time.Time{}
	a.SourceEventKey, b.SourceEventKey = "", ""
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return bytes.Equal(left, right)
}

func sumNonNegative(values ...int64) (int64, bool) {
	const maxInt64 = int64(1<<63 - 1)
	var total int64
	for _, value := range values {
		if value < 0 || value > maxInt64-total {
			return 0, false
		}
		total += value
	}
	return total, true
}

func stableSourceEventKey(prefix string, parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = fmt.Fprintf(hash, "%d:", len(part))
		_, _ = hash.Write([]byte(part))
	}
	return fmt.Sprintf("%s:sha256:%x", prefix, hash.Sum(nil))
}

func int64Ptr(value int64) *int64 {
	return &value
}
