package acp

import (
	"encoding/json"
	"regexp"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// maxMCPStatusText bounds the one-line error shown for a failed MCP server.
const maxMCPStatusText = 200

var (
	// secretAssignment matches KEY=value / token: value pairs whose key names a credential.
	secretAssignment = regexp.MustCompile(`(?i)\b([A-Za-z0-9_.-]*(?:key|token|secret|password|passwd|auth|credential)[A-Za-z0-9_.-]*)\s*[=:]\s*\S+`)
	bearerToken      = regexp.MustCompile(`(?i)\bbearer\s+\S+`)
	urlCredentials   = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://)[^\s/@:]*:[^\s/@]*@`)
	apiKeyLiteral    = regexp.MustCompile(`\b(?:sk|ghp|gho|xox[a-z])-?[A-Za-z0-9_-]{16,}`)
)

// redactMCPText strips credential-looking values so an MCP server error can be
// shown in chat without leaking the env or args it was launched with.
func redactMCPText(text string) string {
	text = bearerToken.ReplaceAllString(text, "Bearer [REDACTED]")
	text = urlCredentials.ReplaceAllString(text, "${1}[REDACTED]@")
	text = secretAssignment.ReplaceAllString(text, "$1=[REDACTED]")
	text = apiKeyLiteral.ReplaceAllString(text, "[REDACTED]")
	text = strings.Join(strings.Fields(text), " ")
	if runes := []rune(text); len(runes) > maxMCPStatusText {
		text = string(runes[:maxMCPStatusText]) + "…"
	}
	return text
}

// mcpServerStatusEvent translates claude-agent-acp's MCP start-failure notice
// (an agent message tagged _meta.claudeCode.kind="mcpServerStatus") into one
// failed system activity. Tallpine does not advertise session notices, so the adapter
// uses the tagged-message form. Healthy statuses and repeats of a server's
// already-reported status produce nothing. The adapter's body is fixed
// boilerplate pointing at /mcp (the chat's notice offers it as buttons), so it is dropped; text is kept
// only when a notice carries a real error message.
func (c *conversation) mcpServerStatusEvent(turnID string, meta map[string]any, text string) (ports.ChatEvent, bool, bool) {
	claude := nestedMap(meta, "claudeCode")
	if claude == nil {
		return ports.ChatEvent{}, false, false
	}
	if kind, _ := claude["kind"].(string); kind != "mcpServerStatus" {
		return ports.ChatEvent{}, false, false
	}
	server, _ := claude["server"].(string)
	status, _ := claude["status"].(string)
	server = strings.TrimSpace(server)
	if server == "" || (status != "failed" && status != "needs-auth" && status != "disconnected") {
		return ports.ChatEvent{}, true, false
	}
	if turnID == "" {
		// The adapter reports from system/init inside a prompt
		// (acp-agent.js:4216), so an empty turn is unexpected; skip without
		// consuming the dedupe slot rather than emit an orphan activity.
		return ports.ChatEvent{}, true, false
	}
	key := server + "\x00" + status
	c.mu.Lock()
	if c.mcpReported == nil {
		c.mcpReported = map[string]bool{}
	}
	seen := c.mcpReported[key]
	c.mcpReported[key] = true
	c.mu.Unlock()
	if seen {
		return ports.ChatEvent{}, true, false
	}
	summary := "MCP server " + server + " failed to start"
	switch status {
	case "needs-auth":
		summary = "MCP server " + server + " needs authentication"
	case "disconnected":
		summary = "MCP server " + server + " disconnected"
	}
	detailMap := map[string]any{"event": "mcp.status", "server": server, "status": status}
	if note := redactMCPText(text); note != "" && !strings.Contains(note, "Run /mcp") {
		detailMap["text"] = note
	}
	detail, _ := json.Marshal(detailMap)
	return ports.ChatEvent{
		Kind:           ports.ChatEventActivityStarted,
		ProviderTurnID: turnID,
		ProviderItemID: c.providerItemID("mcp-status:" + key),
		ActivityKind:   domain.ActivityKindSystem,
		ActivityStatus: domain.ActivityStatusFailed,
		Summary:        summary,
		Detail:         detail,
	}, true, true
}

// mcpNoticeDescription drops the bold "**title:**" label the adapter prefixes.
func mcpNoticeDescription(text string) string {
	if _, rest, ok := strings.Cut(text, ":** "); ok {
		return rest
	}
	return ""
}
