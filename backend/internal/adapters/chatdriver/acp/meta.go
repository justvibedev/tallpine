package acp

func parentToolUseID(meta map[string]any) string {
	claude := nestedMap(meta, "claudeCode")
	if claude == nil {
		return ""
	}
	value, _ := claude["parentToolUseId"].(string)
	return value
}

func terminalOutput(meta map[string]any) string {
	terminal := nestedMap(meta, "terminal_output")
	if terminal == nil {
		return ""
	}
	value, _ := terminal["data"].(string)
	return value
}

func nestedMap(meta map[string]any, key string) map[string]any {
	if meta == nil {
		return nil
	}
	value, _ := meta[key].(map[string]any)
	return value
}

func cloneMeta(meta map[string]any) map[string]any {
	return mergeMeta(nil, meta)
}

func mergeMeta(existing, update map[string]any) map[string]any {
	if len(existing) == 0 && len(update) == 0 {
		return nil
	}
	out := make(map[string]any, len(existing)+len(update))
	for key, value := range existing {
		out[key] = value
	}
	for key, value := range update {
		if current, ok := out[key].(map[string]any); ok {
			if incoming, ok := value.(map[string]any); ok {
				out[key] = mergeMeta(current, incoming)
				continue
			}
		}
		out[key] = value
	}
	return out
}

func copyDetail(target, source map[string]any, sourceKey, targetKey string) {
	if value, ok := source[sourceKey]; ok {
		target[targetKey] = value
	}
}

func number(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	default:
		return 0, false
	}
}
