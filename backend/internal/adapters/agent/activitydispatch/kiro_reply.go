package activitydispatch

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var kiroSessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)

// kiroFinalReply returns the assistant text of the latest turn of a Kiro v3
// session. v3's Stop hook payload has no reply text, but the session log at
// ~/.kiro/sessions/<cwd-hash>/<session_id>/messages.jsonl records each "Say".
func kiroFinalReply(sessionID string) string {
	if !kiroSessionIDPattern.MatchString(sessionID) {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	matches, err := filepath.Glob(filepath.Join(home, ".kiro", "sessions", "*", sessionID, "messages.jsonl"))
	if err != nil || len(matches) == 0 {
		return ""
	}
	file, err := os.Open(matches[0])
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
