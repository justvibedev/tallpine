package claudeacp

import (
	"testing"
	"time"
)

func TestClaudeRateLimitsParsesUsageMeta(t *testing.T) {
	t.Parallel()

	got := claudeRateLimits(map[string]any{"_claude/rateLimit": map[string]any{
		"utilization": 0.8, "resetsAt": float64(time.Now().Add(time.Hour).Unix()),
		"rateLimitType": "five_hour",
	}})
	if got == nil || got.PrimaryUsedPercent != 80 || got.PrimaryResetsInSeconds < 3500 || got.PlanLabel != "five hour" {
		t.Fatalf("limits = %#v", got)
	}
	if got.SecondaryUsedPercent != -1 {
		t.Fatalf("secondary = %v, want -1", got.SecondaryUsedPercent)
	}
	past := claudeRateLimits(map[string]any{"_claude/rateLimit": map[string]any{"resetsAt": float64(1)}})
	if past == nil || past.PrimaryResetsInSeconds != 0 || past.PrimaryUsedPercent != -1 {
		t.Fatalf("past reset = %#v", past)
	}
	if claudeRateLimits(map[string]any{"other": 1}) != nil || claudeRateLimits(nil) != nil {
		t.Fatal("absent extension must yield nil")
	}
}
