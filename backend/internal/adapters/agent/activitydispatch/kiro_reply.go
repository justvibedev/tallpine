package activitydispatch

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"

	"tallpine.eu/tallpine/backend/internal/adapters/agent/kiro"
)

// kiroFinalReply returns the assistant text of the latest turn of a Kiro v3
// session. v3's Stop hook payload has no reply text, but the session log at
// ~/.kiro/sessions/<cwd-hash>/<session_id>/messages.jsonl records each "Say".
func kiroFinalReply(sessionID string) string {
	path := kiro.SessionLogPath(sessionID)
	if path == "" {
		return ""
	}
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = file.Close() }()
	var parts []string
	reader := bufio.NewReaderSize(file, 1<<20)
	for {
		line, readErr := reader.ReadBytes('\n')
		var entry struct {
			Payload struct {
				Type          string `json:"type"`
				Content       string `json:"content"`
				OperationType string `json:"operationType"`
			} `json:"payload"`
		}
		if len(line) > 0 && json.Unmarshal(line, &entry) == nil {
			switch {
			case entry.Payload.Type == "user":
				parts = parts[:0]
			case entry.Payload.Type == "assistant" && entry.Payload.OperationType == "Say" && strings.TrimSpace(entry.Payload.Content) != "":
				parts = append(parts, entry.Payload.Content)
			}
		}
		if readErr != nil {
			break
		}
	}
	return strings.TrimSpace(strings.Join(parts, ""))
}
