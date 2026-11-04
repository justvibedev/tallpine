package kiro

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// TerminalModelUsage reads the credits a terminal-mode Kiro session has spent
// from Kiro's own transcript. Kiro reports no tokens, only credits per turn:
// every `usage_summary` record of
// <sessions root>/<workspace hash>/<session id>/messages.jsonl carries
// payload.promptTurnSummaries = [{unit: "credit", usage: <float>}]. The session
// id is the native id Tallpine's hooks recorded (with or without the `sess_`
// prefix of the directory). ok is false when no transcript is found.
func (p *Plugin) TerminalModelUsage(ctx context.Context, agentSessionID string) ([]domain.ModelUsage, bool, error) {
	agentSessionID = strings.TrimSpace(agentSessionID)
	if agentSessionID == "" {
		return nil, false, nil
	}
	root := p.sessionsRoot
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, false, err
		}
		root = filepath.Join(home, ".kiro", "sessions")
	}
	dir := findSessionDir(root, agentSessionID)
	if dir == "" {
		return nil, false, nil
	}
	file, err := os.Open(filepath.Join(dir, "messages.jsonl")) //nolint:gosec // path built from a sessions root and a matched directory
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = file.Close() }()

	var credits float64
	var turns int64
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		if ctx.Err() != nil {
			return nil, false, ctx.Err()
		}
		var line struct {
			Payload struct {
				Type    string              `json:"type"`
				Summary []PromptTurnSummary `json:"promptTurnSummaries"`
			} `json:"payload"`
		}
		if json.Unmarshal(scanner.Bytes(), &line) != nil || line.Payload.Type != "usage_summary" {
			continue
		}
		if turn, ok := CreditsFromSummaries(line.Payload.Summary); ok {
			credits += turn
			turns++
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, false, err
	}
	model := "kiro"
	if raw, err := os.ReadFile(filepath.Join(dir, "session.json")); err == nil { //nolint:gosec // same directory as above
		var meta struct {
			ModelID string `json:"modelId"`
		}
		if json.Unmarshal(raw, &meta) == nil && meta.ModelID != "" {
			model = meta.ModelID
		}
	}
	return []domain.ModelUsage{{Model: model, Credits: credits, Turns: turns}}, true, nil
}

// findSessionDir returns the transcript directory of the session, or "". The id
// comes from hook payloads, so it is matched by exact directory name (never as a
// glob pattern) and only when it is a plain identifier. When the same session
// directory exists under several workspace hashes the most recently written
// transcript wins, so the result does not depend on directory order.
func findSessionDir(root, agentSessionID string) string {
	if !sessionIDPattern.MatchString(agentSessionID) {
		return ""
	}
	names := []string{agentSessionID, "sess_" + strings.TrimPrefix(agentSessionID, "sess_")}
	hashes, _ := os.ReadDir(root)
	best, bestTime := "", time.Time{}
	for _, name := range names {
		for _, hash := range hashes {
			if !hash.IsDir() {
				continue
			}
			dir := filepath.Join(root, hash.Name(), name)
			info, err := os.Stat(filepath.Join(dir, "messages.jsonl"))
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			if best == "" || info.ModTime().After(bestTime) {
				best, bestTime = dir, info.ModTime()
			}
		}
		if best != "" {
			return best
		}
	}
	return ""
}

var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
