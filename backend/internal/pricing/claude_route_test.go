package pricing

import "testing"

func TestClaudeRouteFromEnv(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"default", nil, "anthropic"},
		{"bedrock", map[string]string{"CLAUDE_CODE_USE_BEDROCK": "1"}, "bedrock"},
		{"vertex", map[string]string{"CLAUDE_CODE_USE_VERTEX": "true"}, "vertex_ai"},
		{"flag spelled on", map[string]string{"CLAUDE_CODE_USE_BEDROCK": " YES "}, "bedrock"},
		{"flag off", map[string]string{"CLAUDE_CODE_USE_BEDROCK": "0"}, "anthropic"},
		{"both cloud flags", map[string]string{"CLAUDE_CODE_USE_BEDROCK": "1", "CLAUDE_CODE_USE_VERTEX": "1"}, UnidentifiedBillingRoute},
		{"foundry only", map[string]string{"CLAUDE_CODE_USE_FOUNDRY": "1"}, UnidentifiedBillingRoute},
		{"foundry beats anthropic base url", map[string]string{"CLAUDE_CODE_USE_FOUNDRY": "1", "ANTHROPIC_BASE_URL": "https://api.anthropic.com"}, UnidentifiedBillingRoute},
		{"anthropic base url", map[string]string{"ANTHROPIC_BASE_URL": "https://api.anthropic.com/v1"}, "anthropic"},
		{"z.ai", map[string]string{"ANTHROPIC_BASE_URL": "https://api.z.ai/api/anthropic"}, "zai"},
		{"schemeless host", map[string]string{"ANTHROPIC_BASE_URL": "api.z.ai"}, "zai"},
		{"unknown host", map[string]string{"ANTHROPIC_BASE_URL": "https://proxy.example.com"}, UnidentifiedBillingRoute},
		{"non-http scheme", map[string]string{"ANTHROPIC_BASE_URL": "ftp://api.anthropic.com"}, UnidentifiedBillingRoute},
		{"unparsable", map[string]string{"ANTHROPIC_BASE_URL": "http://[::1"}, UnidentifiedBillingRoute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClaudeRouteFromEnv(func(k string) string { return tt.env[k] }); got != tt.want {
				t.Fatalf("route = %q, want %q", got, tt.want)
			}
		})
	}
}
