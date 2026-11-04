package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// stopAsyncTaskMethod is claude-agent-acp's own request to stop a background
// task (ASYNC_TASK_STOP_METHOD in acp-agent.js); it needs no Tallpine patch.
const stopAsyncTaskMethod = "_session/async_task/stop"

// StopAsyncTask asks the bridge to stop one background task. A bridge without
// the request answers method-not-found, which reads as "not stopped".
func (c *conversation) StopAsyncTask(ctx context.Context, taskID string) (bool, error) {
	c.mu.Lock()
	sessionID := c.sessionID
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return false, errConversationClosed
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	raw, err := c.conn.CallExtension(ctx, stopAsyncTaskMethod, map[string]any{
		"sessionId":   sessionID,
		"asyncTaskId": taskID,
	})
	if err != nil {
		if isACPMethodNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("ACP %s: %w", stopAsyncTaskMethod, err)
	}
	var response struct {
		Stopped bool `json:"stopped"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return false, fmt.Errorf("decode ACP %s response: %w", stopAsyncTaskMethod, err)
	}
	return response.Stopped, nil
}
