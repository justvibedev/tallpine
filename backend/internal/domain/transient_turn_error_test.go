package domain

import "testing"

func TestIsTransientTurnError(t *testing.T) {
	transient := []string{
		"API Error: Can't reach the API server — check your internet or DNS (ENOTFOUND)",
		"API Error: Connection error.",
		"API Error: Request timed out.",
		"read ECONNRESET",
		"connect ETIMEDOUT 160.79.104.10:443",
		"getaddrinfo EAI_AGAIN api.anthropic.com",
		`API Error: 529 {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`,
		`API Error: 500 {"type":"error","error":{"type":"api_error","message":"Internal server error"}}`,
		"unexpected status 503 Service Unavailable",
		"stream disconnected before completion: error sending request for url (https://chatgpt.com/backend-api/codex/responses)",
		"dial tcp: lookup api.openai.com: no such host",
		"TypeError: fetch failed",
	}
	for _, message := range transient {
		if !IsTransientTurnError(message) {
			t.Errorf("IsTransientTurnError(%q) = false, want true", message)
		}
	}
	permanent := []string{
		"",
		"You've hit your session limit · resets 3pm (Europe/Tallinn)",
		"API Error: 400 prompt is too long: 210000 tokens > 200000 maximum",
		"API Error: 401 authentication_error: invalid x-api-key",
		"API Error: 429 rate_limit_error",
		"the model refused the request",
		"tool failed: exit status 1 after 500 tests",
		"agent process exited",
	}
	for _, message := range permanent {
		if IsTransientTurnError(message) {
			t.Errorf("IsTransientTurnError(%q) = true, want false", message)
		}
	}
}
