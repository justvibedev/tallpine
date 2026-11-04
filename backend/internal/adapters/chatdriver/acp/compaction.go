package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// setAutoCompactWindowMethod is Tallpine's request on the patched claude-agent-acp
// (frontend/scripts/build-acp-runtime-helpers.mjs, patchClaudeAutoCompactWindow).
// A bridge without the patch answers method-not-found.
const setAutoCompactWindowMethod = "_ao/session/set_auto_compact_window"

// SetAutoCompactWindow asks the bridge to change the running session's
// auto-compaction window. Unsupported, locked (the launch environment owns the
// value), a closed session and a lost reply all read as "not applied": the
// caller keeps the value for the next restart rather than failing the chat.
func (c *conversation) SetAutoCompactWindow(ctx context.Context, tokens int) (bool, error) {
	c.mu.Lock()
	sessionID := c.sessionID
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return false, errConversationClosed
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var value any // JSON null clears the live value
	if tokens > 0 {
		value = tokens
	}
	raw, err := c.conn.CallExtension(ctx, setAutoCompactWindowMethod, map[string]any{
		"sessionId": sessionID,
		"tokens":    value,
	})
	if err != nil {
		if isACPMethodNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("ACP %s: %w", setAutoCompactWindowMethod, err)
	}
	var response struct {
		Applied bool `json:"applied"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return false, fmt.Errorf("decode ACP %s response: %w", setAutoCompactWindowMethod, err)
	}
	return response.Applied, nil
}
