package pricing

import (
	"net/url"
	"strings"
)

// ClaudeRouteEnvFlags are the boolean variables that move Claude Code off its
// own Anthropic route onto a cloud provider. Account isolation neutralises the
// same list, so the two cannot drift.
var ClaudeRouteEnvFlags = []string{
	"CLAUDE_CODE_USE_BEDROCK",
	"CLAUDE_CODE_USE_VERTEX",
	"CLAUDE_CODE_USE_FOUNDRY",
}

// ClaudeRouteFromEnv names the billing route a Claude Code process with this
// environment takes: "anthropic", "zai", "bedrock", "vertex_ai", or
// UnidentifiedBillingRoute when the process is routed somewhere Tallpine cannot
// name (Foundry, several cloud flags, or an unknown ANTHROPIC_BASE_URL host).
func ClaudeRouteFromEnv(getenv func(string) string) string {
	bedrock := routeFlagEnabled(getenv("CLAUDE_CODE_USE_BEDROCK"))
	vertex := routeFlagEnabled(getenv("CLAUDE_CODE_USE_VERTEX"))
	foundry := routeFlagEnabled(getenv("CLAUDE_CODE_USE_FOUNDRY"))
	switch {
	case foundry, bedrock && vertex:
		// Certainly not plain Anthropic, and not a route Tallpine prices:
		// this still has to rule out inferring one from the model.
		return UnidentifiedBillingRoute
	case bedrock:
		return "bedrock"
	case vertex:
		return "vertex_ai"
	}
	baseURL := strings.TrimSpace(getenv("ANTHROPIC_BASE_URL"))
	if baseURL == "" {
		return "anthropic"
	}
	// A base URL Tallpine cannot name still rules out inferring one from the
	// model: the session is routed somewhere, and reporting that is the
	// difference between "no hook has run" and "a hook ran and the route is
	// not ours".
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return UnidentifiedBillingRoute
	}
	if parsed.Hostname() == "" && !strings.Contains(baseURL, "://") {
		parsed, err = url.Parse("https://" + baseURL)
	}
	if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" {
		return UnidentifiedBillingRoute
	}
	switch strings.ToLower(parsed.Hostname()) {
	case "api.anthropic.com":
		return "anthropic"
	case "api.z.ai":
		return "zai"
	default:
		return UnidentifiedBillingRoute
	}
}

func routeFlagEnabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
