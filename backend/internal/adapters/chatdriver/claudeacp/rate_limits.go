package claudeacp

import (
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// claudeRateLimits reads the _claude/rateLimit extension that claude-agent-acp
// attaches to a usage_update's _meta.
func claudeRateLimits(meta map[string]any) *ports.ChatRateLimits {
	value := claudeNestedMap(meta, "_claude/rateLimit")
	if value == nil {
		return nil
	}
	limits := &ports.ChatRateLimits{PrimaryUsedPercent: -1, SecondaryUsedPercent: -1}
	if utilization, ok := value["utilization"].(float64); ok {
		// The Claude SDK reports utilization as 0..1.
		limits.PrimaryUsedPercent = utilization * 100
	}
	if resetsAt, ok := value["resetsAt"].(float64); ok {
		remaining := int64(resetsAt) - time.Now().Unix()
		if remaining < 0 {
			remaining = 0
		}
		limits.PrimaryResetsInSeconds = remaining
	}
	if kind, ok := value["rateLimitType"].(string); ok {
		limits.PlanLabel = strings.ReplaceAll(kind, "_", " ")
	}
	return limits
}
