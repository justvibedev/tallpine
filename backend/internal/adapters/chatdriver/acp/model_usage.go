package acp

import "tallpine.eu/tallpine/backend/internal/domain"

// quotaModelUsage reads the per-model spend of one turn from a prompt response's
// _meta. claude-agent-acp (0.99.0, acp-agent.js quotaMeta) sends
//
//	_meta.quota.model_usage = [{model, token_count: {inputTokens, cachedInputTokens,
//	  cachedWriteTokens, outputTokens, totalTokens, ...}}]
//
// where the rows are this turn's increment (not a running total) and inputTokens
// excludes the cache counters. Rows with nothing to report are dropped.
func quotaModelUsage(meta map[string]any) []domain.ModelUsage {
	rows, _ := nestedMap(meta, "quota")["model_usage"].([]any)
	var out []domain.ModelUsage
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		model, _ := row["model"].(string)
		tokens, _ := row["token_count"].(map[string]any)
		if model == "" || tokens == nil {
			continue
		}
		u := domain.ModelUsage{
			Model:               model,
			InputTokens:         count(tokens["inputTokens"]),
			CacheReadTokens:     count(tokens["cachedInputTokens"]),
			CacheCreationTokens: count(tokens["cachedWriteTokens"]),
			OutputTokens:        count(tokens["outputTokens"]),
		}
		if u.InputTokens+u.CacheReadTokens+u.CacheCreationTokens+u.OutputTokens > 0 {
			out = append(out, u)
		}
	}
	return out
}

func count(value any) int64 {
	n, _ := number(value)
	if n < 0 {
		return 0
	}
	return int64(n)
}
