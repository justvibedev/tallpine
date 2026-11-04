package acp

import "tallpine.eu/tallpine/backend/internal/ports"

// stubUsageRateLimits stands in for a binding's Config.UsageRateLimits: the
// generic driver only forwards what the hook returns.
func stubUsageRateLimits(meta map[string]any) *ports.ChatRateLimits {
	if _, ok := meta["_test/rateLimit"]; !ok {
		return nil
	}
	return &ports.ChatRateLimits{PrimaryUsedPercent: 80, PrimaryResetsInSeconds: 3600}
}
