package kiro

import (
	"os"
	"path/filepath"
)

const sessionLogFile = "messages.jsonl"

func defaultSessionsRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".kiro", "sessions"), nil
}

// SessionLogPath returns the absolute path of the session's messages.jsonl
// under ~/.kiro/sessions, or "" when the session directory is unknown. The
// file itself may not exist yet: Kiro creates the directory (session.json) at
// session/new and the log with the first turn, so the directory is enough.
// Activity dispatch uses it both to read a turn's reply and to record the
// transcript path the failed-turn observer watches.
func SessionLogPath(agentSessionID string) string {
	root, err := defaultSessionsRoot()
	if err != nil {
		return ""
	}
	dir := findSessionDirWith(root, agentSessionID, sessionLogFile)
	if dir == "" {
		dir = findSessionDirWith(root, agentSessionID, "session.json")
	}
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, sessionLogFile)
}
