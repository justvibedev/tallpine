package domain

import (
	"regexp"
	"strings"
)

// transientTurnErrorPatterns recognize provider failures caused by the network
// or a briefly unavailable API rather than by the request itself: DNS and
// socket errors, timeouts, a dropped stream, and 5xx/overloaded replies. Each
// pattern names the failure precisely, so assistant prose and quoted code that
// merely mentions a number do not match.
var transientTurnErrorPatterns = []*regexp.Regexp{
	// Node/libuv socket and resolver codes (Claude Code, ACP agents).
	regexp.MustCompile(`\b(?:ENOTFOUND|EAI_AGAIN|ECONNRESET|ECONNREFUSED|ECONNABORTED|ETIMEDOUT|ESOCKETTIMEDOUT|ENETUNREACH|ENETDOWN|EHOSTUNREACH|EPIPE|UND_ERR_SOCKET|UND_ERR_CONNECT_TIMEOUT)\b`),
	// Claude Code's own wording for an unreachable API.
	regexp.MustCompile(`(?i)can'?t reach the API server|API Error: *Connection error|API Error: *Request timed out|fetch failed|socket hang up|network (?:error|is unreachable)|no internet connection|internet connection appears to be offline`),
	// Codex and Go-side transport failures.
	regexp.MustCompile(`(?i)stream disconnected before completion|error sending request|connection (?:reset|refused|closed) by peer|unexpected EOF|i/o timeout|no such host|temporary failure in name resolution|TLS handshake timeout|dial tcp`),
	// Server-side availability: an API error with a 5xx status, or an overload.
	regexp.MustCompile(`(?i)API Error: *5\d\d\b|\bstatus(?: code)?:? *5\d\d\b|\b5\d\d (?:Internal Server Error|Bad Gateway|Service Unavailable|Gateway Timeout)\b|overloaded_error|\boverloaded\b|"type": *"api_error"`),
}

// IsTransientTurnError reports whether a failed turn's error is a transient
// network or API-availability failure that is safe to retry unattended. A
// quota refusal is never transient here: usage limits have their own durable
// wait-for-reset path, and retrying one sooner would only fail again.
func IsTransientTurnError(message string) bool {
	message = strings.TrimSpace(message)
	if message == "" || ParseConversationUsageLimit(message, nil) != nil {
		return false
	}
	for _, pattern := range transientTurnErrorPatterns {
		if pattern.MatchString(message) {
			return true
		}
	}
	return false
}
